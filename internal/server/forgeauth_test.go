package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dougkirkley/dross/internal/forge"
	"github.com/dougkirkley/dross/internal/storage"
)

// fakeForge is a git host that answers from what a test put in it, and records
// what it was asked. Every field is read by at least one test below.
type fakeForge struct {
	name string

	token      forge.Token
	tokenErr   error
	repos      []forge.Repo
	reposErr   error
	refresh    forge.Token
	refreshErr error

	authRedirect     string // the redirect URI AuthCodeURL was given
	exchangeCode     string
	exchangeRedirect string
	refreshes        int
}

func (f *fakeForge) Name() string { return f.name }

func (f *fakeForge) AuthCodeURL(state, redirectURI string) string {
	f.authRedirect = redirectURI
	return "https://fake.test/authorize?state=" + url.QueryEscape(state) +
		"&redirect_uri=" + url.QueryEscape(redirectURI)
}

func (f *fakeForge) Exchange(_ context.Context, code, redirectURI string) (forge.Token, error) {
	f.exchangeCode, f.exchangeRedirect = code, redirectURI
	return f.token, f.tokenErr
}

func (f *fakeForge) Refresh(context.Context, string) (forge.Token, error) {
	f.refreshes++
	return f.refresh, f.refreshErr
}

func (f *fakeForge) Repos(context.Context, string) ([]forge.Repo, error) {
	return f.repos, f.reposErr
}

func (f *fakeForge) CloneURL(repoURL, _ string) string { return repoURL }

// installable is a host with an install step, which is GitHub's shape.
type installable struct {
	*fakeForge
	url string
}

func (i installable) InstallURL() string { return i.url }

// forgeServer is a server with a credential store and no SSO, so a test can put
// an identity on the request itself. The routes and the checks are the real ones.
func forgeServer(t *testing.T) (*Server, *ForgeTokens) {
	t.Helper()
	forge.Reset()
	t.Cleanup(forge.Reset)
	tokens, err := NewForgeTokens(t.TempDir(), []byte("a cookie secret"))
	if err != nil {
		t.Fatal(err)
	}
	srv := New(storage.NewSessionStore(t.TempDir()),
		NewManager(&fakeProvider{}, nil, "m", t.TempDir()), nil, testArts(t))
	srv.SetForges(tokens)
	return srv, tokens
}

func get(t *testing.T, srv *Server, path, who string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, as(httptest.NewRequest("GET", path, nil), who))
	return w
}

// connect walks the browser half of the flow and returns the state cookie and
// the state the host was asked to echo back.
func connect(t *testing.T, srv *Server, path, who string) (*http.Cookie, string) {
	t.Helper()
	w := get(t, srv, path, who)
	if w.Code != http.StatusFound {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("connect sent the browser to %q: %v", w.Header().Get("Location"), err)
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == forgeStateCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("connect set no state cookie, so the callback has nothing to check against")
	}
	return cookie, loc.Query().Get("state")
}

func callback(t *testing.T, srv *Server, path, who string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := as(httptest.NewRequest("GET", path, nil), who)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w
}

// The list is the whole picker in one request: which hosts exist, which the
// caller has already connected, and where to install the ones that need it.
func TestForgeListSaysWhatIsConnected(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(installable{&fakeForge{name: "github"}, "https://github.com/apps/dross/installations/new"})
	forge.Register(&fakeForge{name: "gitlab"})
	if err := tokens.Save("doug@example.com", "gitlab", forge.Token{Access: "glpat"}); err != nil {
		t.Fatal(err)
	}

	w := get(t, srv, "/api/forges", "doug@example.com")
	var body struct {
		Hosts []struct {
			Name       string `json:"name"`
			Connected  bool   `json:"connected"`
			InstallURL string `json:"install_url"`
		} `json:"hosts"`
		CanConnect bool `json:"can_connect"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.CanConnect {
		t.Error("a server with a credential store said accounts cannot be connected")
	}
	if len(body.Hosts) != 2 {
		t.Fatalf("hosts: %+v", body.Hosts)
	}
	gh, gl := body.Hosts[0], body.Hosts[1]
	if gh.Name != "github" || gh.Connected {
		t.Errorf("github: %+v, want an unconnected github first", gh)
	}
	if gh.InstallURL == "" {
		t.Error("github offered no install URL, which is the step after authorizing")
	}
	if gl.Name != "gitlab" || !gl.Connected {
		t.Errorf("gitlab: %+v, want it connected", gl)
	}
	if gl.InstallURL != "" {
		t.Errorf("gitlab offered an install URL (%q); it has no such step", gl.InstallURL)
	}
}

// A different user's credential is not this user's connection.
func TestForgeListIsPerUser(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(&fakeForge{name: "gitlab"})
	_ = tokens.Save("doug@example.com", "gitlab", forge.Token{Access: "glpat"})

	w := get(t, srv, "/api/forges", "stranger@example.com")
	if strings.Contains(w.Body.String(), `"connected":true`) {
		t.Errorf("a stranger was told they are connected: %s", w.Body.String())
	}
}

// The whole round trip: connect, come back with the code, and the token is
// stored under the identity that started it.
func TestForgeCallbackStoresTheToken(t *testing.T) {
	srv, tokens := forgeServer(t)
	f := &fakeForge{name: "gitlab", token: forge.Token{Access: "glpat-new", Refresh: "r1"}}
	forge.Register(f)
	srv.SetPublicURL("https://dross.sh/")

	cookie, state := connect(t, srv, "/api/forges/gitlab/connect?next=/settings", "doug@example.com")
	w := callback(t, srv, "/api/forges/gitlab/callback?code=abc123&state="+state, "doug@example.com", cookie)

	if w.Code != http.StatusFound {
		t.Fatalf("callback: %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != "/settings?forge=gitlab" {
		t.Errorf("callback sent the browser to %q, want the page it came from", got)
	}
	if f.exchangeCode != "abc123" {
		t.Errorf("exchanged code %q", f.exchangeCode)
	}
	// Both halves must name the same redirect URI or the host refuses the
	// exchange, and it has to be the public origin rather than the request's.
	want := "https://dross.sh/api/forges/gitlab/callback"
	if f.authRedirect != want || f.exchangeRedirect != want {
		t.Errorf("redirect URIs: authorize %q, exchange %q, want %q", f.authRedirect, f.exchangeRedirect, want)
	}
	tok, ok := tokens.Load("doug@example.com", "gitlab")
	if !ok || tok.Access != "glpat-new" {
		t.Errorf("stored %+v (found: %v), want the exchanged token", tok, ok)
	}
}

// The state cookie is one flow's, and the flow belongs to whoever started it.
// Without this a signed-in user could be walked through somebody else's
// authorization and end up with their account attached.
func TestForgeCallbackRefusesAnotherIdentity(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(&fakeForge{name: "gitlab", token: forge.Token{Access: "glpat"}})

	cookie, state := connect(t, srv, "/api/forges/gitlab/connect", "attacker@example.com")
	w := callback(t, srv, "/api/forges/gitlab/callback?code=abc&state="+state, "victim@example.com", cookie)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("callback: %d, want it refused", w.Code)
	}
	if _, ok := tokens.Load("victim@example.com", "gitlab"); ok {
		t.Error("the victim was connected to an account they never authorized")
	}
}

func TestForgeCallbackRefusesABadState(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(&fakeForge{name: "gitlab", token: forge.Token{Access: "glpat"}})
	cookie, _ := connect(t, srv, "/api/forges/gitlab/connect", "doug@example.com")

	for _, c := range []struct {
		name   string
		path   string
		cookie *http.Cookie
	}{
		{"no cookie", "/api/forges/gitlab/callback?code=abc&state=whatever", nil},
		{"state does not match the cookie", "/api/forges/gitlab/callback?code=abc&state=whatever", cookie},
		{"unsigned cookie", "/api/forges/gitlab/callback?code=abc&state=whatever",
			&http.Cookie{Name: forgeStateCookie, Value: "not.signed"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if w := callback(t, srv, c.path, "doug@example.com", c.cookie); w.Code != http.StatusBadRequest {
				t.Errorf("callback: %d, want it refused", w.Code)
			}
		})
	}
	if _, ok := tokens.Load("doug@example.com", "gitlab"); ok {
		t.Error("a refused callback stored a token anyway")
	}
}

// Declining on the host's page is a decision, not an error page.
func TestForgeCallbackCarriesADeclineBackToTheApp(t *testing.T) {
	srv, _ := forgeServer(t)
	forge.Register(&fakeForge{name: "gitlab"})

	cookie, state := connect(t, srv, "/api/forges/gitlab/connect?next=/repos", "doug@example.com")
	w := callback(t, srv, "/api/forges/gitlab/callback?error=access_denied&state="+state, "doug@example.com", cookie)

	if w.Code != http.StatusFound {
		t.Fatalf("callback: %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != "/repos?forge_error=access_denied" {
		t.Errorf("declined callback went to %q", got)
	}
}

// ?next= decides where the browser lands after the flow, which makes it an open
// redirect if it is believed.
func TestForgeNextStaysOnThisSite(t *testing.T) {
	srv, _ := forgeServer(t)
	forge.Register(&fakeForge{name: "gitlab", token: forge.Token{Access: "glpat"}})

	for _, next := range []string{"https://evil.test/", "//evil.test/", "/\\evil.test"} {
		cookie, state := connect(t, srv, "/api/forges/gitlab/connect?next="+url.QueryEscape(next), "doug@example.com")
		w := callback(t, srv, "/api/forges/gitlab/callback?code=abc&state="+state, "doug@example.com", cookie)
		if got := w.Header().Get("Location"); got != "/?forge=gitlab" {
			t.Errorf("next=%q sent the browser to %q", next, got)
		}
	}
}

func TestForgeReposNeedsAConnection(t *testing.T) {
	srv, _ := forgeServer(t)
	forge.Register(&fakeForge{name: "gitlab"})

	if w := get(t, srv, "/api/forges/gitlab/repos", "doug@example.com"); w.Code != http.StatusForbidden {
		t.Errorf("repos without a connection: %d, want 403", w.Code)
	}
}

func TestForgeReposLists(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(&fakeForge{name: "gitlab", repos: []forge.Repo{
		{FullName: "doug/dross", CloneURL: "https://gitlab.com/doug/dross.git", Branch: "main"},
	}})
	_ = tokens.Save("doug@example.com", "gitlab", forge.Token{Access: "glpat"})

	w := get(t, srv, "/api/forges/gitlab/repos", "doug@example.com")
	if w.Code != http.StatusOK {
		t.Fatalf("repos: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Repos []forge.Repo `json:"repos"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Repos) != 1 || body.Repos[0].FullName != "doug/dross" {
		t.Errorf("repos: %+v", body.Repos)
	}
	// The listing is handed to a client and cached; a credential in it would
	// outlive every place it was written down.
	if strings.Contains(w.Body.String(), "glpat") {
		t.Errorf("the repository list carried the access token: %s", w.Body.String())
	}
}

// Authorized but not installed is a normal GitHub state with its own next step,
// so it is an empty list and the link that fixes it.
func TestForgeReposOffersTheInstallLink(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(installable{
		&fakeForge{name: "github", reposErr: forge.ErrNoInstallation},
		"https://github.com/apps/dross/installations/new",
	})
	_ = tokens.Save("doug@example.com", "github", forge.Token{Access: "gho"})

	w := get(t, srv, "/api/forges/github/repos", "doug@example.com")
	if w.Code != http.StatusOK {
		t.Fatalf("repos: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Repos      []forge.Repo `json:"repos"`
		InstallURL string       `json:"install_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Repos) != 0 || body.InstallURL == "" {
		t.Errorf("body: %+v, want no repositories and somewhere to go", body)
	}
}

// GitLab's tokens last two hours, so this is the ordinary path rather than the
// exception, and the renewed token has to be kept or the next call repeats it.
func TestForgeReposRefreshesAnExpiredToken(t *testing.T) {
	srv, tokens := forgeServer(t)
	f := &fakeForge{
		name:    "gitlab",
		refresh: forge.Token{Access: "glpat-2", Expiry: time.Now().Add(2 * time.Hour)},
		repos:   []forge.Repo{{FullName: "doug/dross"}},
	}
	forge.Register(f)
	_ = tokens.Save("doug@example.com", "gitlab",
		forge.Token{Access: "glpat-1", Refresh: "r1", Expiry: time.Now().Add(-time.Hour)})

	if w := get(t, srv, "/api/forges/gitlab/repos", "doug@example.com"); w.Code != http.StatusOK {
		t.Fatalf("repos: %d %s", w.Code, w.Body.String())
	}
	if f.refreshes != 1 {
		t.Errorf("refreshed %d times, want once", f.refreshes)
	}
	tok, _ := tokens.Load("doug@example.com", "gitlab")
	if tok.Access != "glpat-2" {
		t.Errorf("stored access %q, want the refreshed one", tok.Access)
	}
	// The host returned no new refresh token, which means the old one still
	// stands. Dropping it would strand the account one expiry later.
	if tok.Refresh != "r1" {
		t.Errorf("stored refresh %q, want the one still valid", tok.Refresh)
	}
}

// A refresh the host refuses leaves nothing usable, and a credential that can
// only fail is worse than none: it keeps the client showing "connected".
func TestForgeReposDropsACredentialItCannotRenew(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(&fakeForge{name: "gitlab", refreshErr: forge.ErrNoRefresh})
	_ = tokens.Save("doug@example.com", "gitlab",
		forge.Token{Access: "glpat-1", Expiry: time.Now().Add(-time.Hour)})

	if w := get(t, srv, "/api/forges/gitlab/repos", "doug@example.com"); w.Code != http.StatusForbidden {
		t.Fatalf("repos: %d, want 403 and a fresh connection", w.Code)
	}
	if _, ok := tokens.Load("doug@example.com", "gitlab"); ok {
		t.Error("a credential that cannot be renewed was kept")
	}
}

func TestForgeDisconnect(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(&fakeForge{name: "gitlab"})
	_ = tokens.Save("doug@example.com", "gitlab", forge.Token{Access: "glpat"})

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, as(httptest.NewRequest("DELETE", "/api/forges/gitlab", nil), "doug@example.com"))
	if w.Code != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", w.Code, w.Body.String())
	}
	if _, ok := tokens.Load("doug@example.com", "gitlab"); ok {
		t.Error("the credential survived being disconnected")
	}
}

// Disconnecting is a state change a signed-in browser can be made to perform by
// another site, so the same origin check the admin endpoints use applies.
func TestForgeDisconnectRefusesAForeignOrigin(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(&fakeForge{name: "gitlab"})
	srv.SetPublicURL("https://dross.sh")
	_ = tokens.Save("doug@example.com", "gitlab", forge.Token{Access: "glpat"})

	r := as(httptest.NewRequest("DELETE", "/api/forges/gitlab", nil), "doug@example.com")
	r.Header.Set("Origin", "https://evil.test")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("disconnect from a foreign origin: %d, want 403", w.Code)
	}
	if _, ok := tokens.Load("doug@example.com", "gitlab"); !ok {
		t.Error("a refused disconnect deleted the credential anyway")
	}
}

func TestForgeUnknownHost(t *testing.T) {
	srv, _ := forgeServer(t)
	if w := get(t, srv, "/api/forges/bitbucket/connect", "doug@example.com"); w.Code != http.StatusNotFound {
		t.Errorf("unconfigured host: %d, want 404", w.Code)
	}
}

// No cookie secret means no key to encrypt a credential with. Refuse at the
// start of the flow rather than after a trip through the host.
func TestForgeWithoutAStoreRefusesToConnect(t *testing.T) {
	forge.Reset()
	t.Cleanup(forge.Reset)
	forge.Register(&fakeForge{name: "gitlab"})
	srv := New(storage.NewSessionStore(t.TempDir()),
		NewManager(&fakeProvider{}, nil, "m", t.TempDir()), nil, testArts(t))

	if w := get(t, srv, "/api/forges/gitlab/connect", "doug@example.com"); w.Code != http.StatusNotFound {
		t.Errorf("connect with no store: %d, want 404", w.Code)
	}
	w := get(t, srv, "/api/forges", "doug@example.com")
	if !strings.Contains(w.Body.String(), `"can_connect":false`) {
		t.Errorf("the list did not say connecting is impossible: %s", w.Body.String())
	}
}

// With SSO off there is no identity to file a credential under, and "" is not
// one — it would be a single shared account for everyone who reached the server.
func TestForgeNeedsAnIdentity(t *testing.T) {
	srv, _ := forgeServer(t)
	forge.Register(&fakeForge{name: "gitlab"})
	if w := get(t, srv, "/api/forges/gitlab/connect", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("connect with no identity: %d, want 401", w.Code)
	}
}

// Installing from GitHub's own page redirects here with a code this server
// never asked for and no cookie to check it against. That person has done the
// right thing and should end up connected, not on an error.
func TestForgeCallbackFromTheInstallPageStartsTheFlow(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(&fakeForge{name: "github", token: forge.Token{Access: "gho"}})

	w := callback(t, srv,
		"/api/forges/github/callback?code=abc&installation_id=42&setup_action=install",
		"doug@example.com", nil)

	if w.Code != http.StatusFound {
		t.Fatalf("callback: %d %s, want a redirect into the flow", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != "/api/forges/github/connect" {
		t.Errorf("sent the browser to %q", got)
	}
	// The code that arrived is dropped, not exchanged. Nothing ties it to the
	// identity holding this session.
	if _, ok := tokens.Load("doug@example.com", "github"); ok {
		t.Error("a code with no state behind it was exchanged and stored")
	}
}

// A stateless callback that is not an install is still refused, so a code
// planted by somebody else cannot start a flow on a signed-in user's behalf.
func TestForgeCallbackWithNoStateAndNoInstallIsRefused(t *testing.T) {
	srv, _ := forgeServer(t)
	forge.Register(&fakeForge{name: "github", token: forge.Token{Access: "gho"}})

	w := callback(t, srv, "/api/forges/github/callback?code=abc&state=made-up",
		"doug@example.com", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("callback: %d, want it refused", w.Code)
	}
}
