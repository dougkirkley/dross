package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The two hosts want the credential in different places, and getting it wrong
// fails as "authentication failed" rather than as anything naming the cause.
func TestCloneURLsCarryTheRightUsername(t *testing.T) {
	gh := &GitHub{}
	if got := gh.CloneURL("https://github.com/doug/dross.git", "tok"); got !=
		"https://x-access-token:tok@github.com/doug/dross.git" {
		t.Errorf("github clone URL = %q", got)
	}
	gl := &GitLab{}
	if got := gl.CloneURL("https://gitlab.com/doug/dross.git", "tok"); got !=
		"https://oauth2:tok@gitlab.com/doug/dross.git" {
		t.Errorf("gitlab clone URL = %q", got)
	}
}

// A refused exchange arrives as a 200 from GitHub, so the status alone says
// nothing. Missing this shipped an empty credential onward and failed at the
// clone, a long way from the thing that went wrong.
func TestGitHubReportsAnErrorInA200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":"bad_verification_code","error_description":"The code passed is incorrect or expired."}`))
	}))
	defer srv.Close()

	g := &GitHub{Config: Config{ClientID: "id", ClientSecret: "sec", BaseURL: srv.URL}, HTTP: srv.Client()}
	_, err := g.Exchange(context.Background(), "code", "https://dross.sh/cb")
	if err == nil {
		t.Fatal("a refused exchange was reported as success")
	}
	if !strings.Contains(err.Error(), "incorrect or expired") {
		t.Errorf("error = %v; want the description GitHub sent", err)
	}
}

// Without Accept: application/json GitHub answers form-encoded, which decodes
// into an empty struct and looks like success.
func TestGitHubAsksForJSON(t *testing.T) {
	var accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		_, _ = w.Write([]byte(`{"access_token":"t"}`))
	}))
	defer srv.Close()

	g := &GitHub{Config: Config{BaseURL: srv.URL}, HTTP: srv.Client()}
	if _, err := g.Exchange(context.Background(), "c", "r"); err != nil {
		t.Fatal(err)
	}
	if accept != "application/json" {
		t.Errorf("Accept = %q", accept)
	}
}

// GitLab's tokens last two hours, so the expiry has to survive the exchange or
// nothing ever refreshes and every session started the next day fails.
func TestGitLabKeepsTheExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"r","expires_in":7200}`))
	}))
	defer srv.Close()

	g := &GitLab{Config: Config{BaseURL: srv.URL}, HTTP: srv.Client()}
	tok, err := g.Exchange(context.Background(), "c", "r")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Refresh != "r" {
		t.Errorf("refresh token = %q", tok.Refresh)
	}
	if d := time.Until(tok.Expiry); d < 100*time.Minute || d > 121*time.Minute {
		t.Errorf("expiry in %s; want about two hours", d)
	}
	if tok.Expired() {
		t.Error("a fresh two-hour token reported itself expired")
	}
}

// A token with no expiry is GitHub's normal case and must not be treated as
// lapsed — doing so sends every clone through a refresh that cannot work.
func TestTokenWithNoExpiryIsNotExpired(t *testing.T) {
	if (Token{Access: "a"}).Expired() {
		t.Error("a token with no stated expiry was treated as expired")
	}
	if !(Token{Access: "a", Expiry: time.Now().Add(-time.Hour)}).Expired() {
		t.Error("a lapsed token was treated as live")
	}
	// The minute of slack: a token expiring within the second would otherwise
	// be used for a request that outlives it.
	if !(Token{Access: "a", Expiry: time.Now().Add(10 * time.Second)}).Expired() {
		t.Error("a token about to lapse was not refreshed")
	}
}

// Refresh on a host that does not expire tokens has nothing to do, and saying
// so beats a request that fails with "bad refresh token".
func TestRefreshWithoutATokenIsNamed(t *testing.T) {
	if _, err := (&GitHub{}).Refresh(context.Background(), ""); err != ErrNoRefresh {
		t.Errorf("github: %v", err)
	}
	if _, err := (&GitLab{}).Refresh(context.Background(), ""); err != ErrNoRefresh {
		t.Errorf("gitlab: %v", err)
	}
}

// The authorize URL is the one thing here a user sees fail visibly: a wrong
// endpoint sends them to a 404 on the host's own site.
func TestAuthCodeURLs(t *testing.T) {
	gh := (&GitHub{Config: Config{ClientID: "abc"}}).AuthCodeURL("st", "https://dross.sh/cb")
	if !strings.HasPrefix(gh, "https://github.com/login/oauth/authorize?") {
		t.Errorf("github authorize URL = %q", gh)
	}
	gl := (&GitLab{Config: Config{ClientID: "abc"}}).AuthCodeURL("st", "https://dross.sh/cb")
	if !strings.HasPrefix(gl, "https://gitlab.com/oauth/authorize?") {
		t.Errorf("gitlab authorize URL = %q", gl)
	}
	// GitLab refuses the request without it; GitHub defaults to code and does
	// not, which is exactly why it is easy to leave out.
	if u, _ := url.Parse(gl); u.Query().Get("response_type") != "code" {
		t.Error("gitlab authorize URL is missing response_type=code")
	}
	for _, u := range []string{gh, gl} {
		p, _ := url.Parse(u)
		if p.Query().Get("state") != "st" {
			t.Errorf("%s dropped the state", p.Host)
		}
		if p.Query().Get("redirect_uri") != "https://dross.sh/cb" {
			t.Errorf("%s dropped the redirect URI", p.Host)
		}
	}
}

// dross pushes nothing, so it must not ask for permission to.
func TestScopesDoNotAskForWriteAccess(t *testing.T) {
	gl, _ := url.Parse((&GitLab{}).AuthCodeURL("s", "r"))
	if strings.Contains(gl.Query().Get("scope"), "write") {
		t.Errorf("gitlab scope = %q; dross never pushes", gl.Query().Get("scope"))
	}
}

// A repository list is handed to clients and cached, so it must not carry the
// credential that produced it.
func TestListedReposCarryNoCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/user/repos") {
			_, _ = w.Write([]byte(`[{"full_name":"doug/dross","clone_url":"https://github.com/doug/dross.git","default_branch":"main","private":true}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"path_with_namespace":"doug/homelab","http_url_to_repo":"https://gitlab.com/doug/homelab.git","default_branch":"main","visibility":"private"}]`))
	}))
	defer srv.Close()

	gh, err := (&GitHub{Config: Config{BaseURL: srv.URL}, HTTP: srv.Client()}).
		Repos(context.Background(), "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	if len(gh) != 1 || gh[0].FullName != "doug/dross" || !gh[0].Private {
		t.Fatalf("github repos = %+v", gh)
	}
	gl, err := (&GitLab{Config: Config{BaseURL: srv.URL}, HTTP: srv.Client()}).
		Repos(context.Background(), "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	if len(gl) != 1 || gl[0].FullName != "doug/homelab" {
		t.Fatalf("gitlab repos = %+v", gl)
	}
	for _, r := range append(gh, gl...) {
		if strings.Contains(r.CloneURL, "secret-token") || strings.Contains(r.CloneURL, "@") {
			t.Errorf("clone URL carries a credential: %q", r.CloneURL)
		}
	}
}

// GitLab calls anything that is not public private, because "internal" is
// private to everyone outside the instance.
func TestGitLabInternalIsPrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"path_with_namespace":"a/b","visibility":"internal"}]`))
	}))
	defer srv.Close()
	got, err := (&GitLab{Config: Config{BaseURL: srv.URL}, HTTP: srv.Client()}).
		Repos(context.Background(), "t")
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].Private {
		t.Error("an internal project was listed as public")
	}
}

// A host with no OAuth application registered cannot be offered.
func TestConfiguredNeedsBothHalves(t *testing.T) {
	if (Config{ClientID: "id"}).Configured() {
		t.Error("a client id with no secret was treated as configured")
	}
	if !(Config{ClientID: "id", ClientSecret: "s"}).Configured() {
		t.Error("a complete config was not")
	}
}

func TestRegistryListsWhatIsConfigured(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	if len(Names()) != 0 {
		t.Fatal("registry started dirty")
	}
	Register(&GitLab{})
	Register(&GitHub{})
	if got := Names(); len(got) != 2 || got[0] != "github" || got[1] != "gitlab" {
		t.Errorf("Names() = %v; want both, in a stable order", got)
	}
	if _, ok := Get("github"); !ok {
		t.Error("github not retrievable")
	}
	if _, ok := Get("bitbucket"); ok {
		t.Error("an unregistered host was returned")
	}
}
