package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/internal/forge"
	"github.com/dougkirkley/dross/internal/storage"
)

// cloneCall is what the clone was asked to do, captured instead of run.
type cloneCall struct {
	dir, url, branch string
	called           bool
}

// watchClone replaces the real clone for one test.
func watchClone(t *testing.T, err error) *cloneCall {
	t.Helper()
	got := &cloneCall{}
	real := cloneRepo
	cloneRepo = func(_ context.Context, dir, url, branch string) error {
		got.dir, got.url, got.branch, got.called = dir, url, branch, true
		return err
	}
	t.Cleanup(func() { cloneRepo = real })
	return got
}

func createSession(t *testing.T, srv *Server, body, who string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/sessions", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, as(r, who))
	return w
}

// The repository a session was started on, cloned into its workspace and used
// as the session's label.
func TestCreateSessionClonesThePickedRepository(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(&fakeForge{name: "github", repos: []forge.Repo{
		{FullName: "doug/dross", CloneURL: "https://github.com/doug/dross.git", Branch: "main"},
	}})
	_ = tokens.Save("doug@example.com", "github", forge.Token{Access: "gho_abc"})
	clone := watchClone(t, nil)

	w := createSession(t, srv, `{"forge":"github","repo":"doug/dross"}`, "doug@example.com")
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if !clone.called {
		t.Fatal("the session was created without cloning anything")
	}
	// The workspace root, not a directory beneath it: the session is meant to
	// be in the repository, not next to it.
	if clone.dir == "" || !strings.HasPrefix(filepath.Base(clone.dir), "ses_") {
		t.Errorf("cloned into %q, want the session's own workspace root", clone.dir)
	}
	// The default branch comes from the listing, so a client that does not ask
	// for one still gets the branch the repository itself considers current.
	if clone.branch != "main" {
		t.Errorf("checked out %q, want the repository's default", clone.branch)
	}
	var snap map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap["workspace"] != "doug/dross" {
		t.Errorf("session labelled %q, want the repository it is on", snap["workspace"])
	}
}

// The clone URL is built by the server from the host's own listing. A client
// that could name it would be naming where this server sends the user's token.
func TestCreateSessionWillNotCloneAnAddressTheClientNames(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(&fakeForge{
		name:  "github",
		repos: []forge.Repo{{FullName: "doug/dross", CloneURL: "https://github.com/doug/dross.git"}},
	})
	_ = tokens.Save("doug@example.com", "github", forge.Token{Access: "gho_abc"})
	clone := watchClone(t, nil)

	body := `{"forge":"github","repo":"doug/dross","clone_url":"https://evil.test/x.git"}`
	if w := createSession(t, srv, body, "doug@example.com"); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(clone.url, "evil.test") {
		t.Errorf("the request chose the address to clone from: %q", clone.url)
	}
}

// A repository the connected account cannot see is not a repository, whoever
// asked for it — including one that exists and was simply never granted.
func TestCreateSessionRefusesAnUnseenRepository(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(&fakeForge{name: "github", repos: []forge.Repo{{FullName: "doug/dross"}}})
	_ = tokens.Save("doug@example.com", "github", forge.Token{Access: "gho_abc"})
	clone := watchClone(t, nil)

	w := createSession(t, srv, `{"forge":"github","repo":"someone/private"}`, "doug@example.com")
	if w.Code != http.StatusNotFound {
		t.Fatalf("create: %d, want 404", w.Code)
	}
	if clone.called {
		t.Error("a repository the account cannot see was cloned anyway")
	}
	if sessions := listSessions(t, srv, "doug@example.com"); len(sessions) != 0 {
		t.Errorf("a refused create left %d sessions behind", len(sessions))
	}
}

func TestCreateSessionOnARepoNeedsAConnection(t *testing.T) {
	srv, _ := forgeServer(t)
	forge.Register(&fakeForge{name: "github"})

	w := createSession(t, srv, `{"forge":"github","repo":"doug/dross"}`, "doug@example.com")
	if w.Code != http.StatusForbidden {
		t.Fatalf("create: %d, want 403 and an invitation to connect", w.Code)
	}
}

// A failed clone is a failed session. Leaving one behind would leave an empty
// workspace claiming to be the repository the user asked for.
func TestCreateSessionFailsWithTheClone(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(&fakeForge{name: "github", repos: []forge.Repo{
		{FullName: "doug/dross", CloneURL: "https://github.com/doug/dross.git"},
	}})
	_ = tokens.Save("doug@example.com", "github", forge.Token{Access: "gho_abc"})
	watchClone(t, context.DeadlineExceeded)

	w := createSession(t, srv, `{"forge":"github","repo":"doug/dross"}`, "doug@example.com")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("create: %d, want the failure reported", w.Code)
	}
	if sessions := listSessions(t, srv, "doug@example.com"); len(sessions) != 0 {
		t.Errorf("a session survived a clone that failed: %d", len(sessions))
	}
}

// A chat session has no workspace at all, so the two requests contradict rather
// than combine.
func TestChatSessionTakesNoRepository(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(&fakeForge{name: "github", repos: []forge.Repo{{FullName: "doug/dross"}}})
	_ = tokens.Save("doug@example.com", "github", forge.Token{Access: "gho_abc"})

	w := createSession(t, srv, `{"chat":true,"forge":"github","repo":"doug/dross"}`, "doug@example.com")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create: %d, want 400", w.Code)
	}
}

// Sessions with no repository are every session that existed before this, and
// they still start on an empty workspace.
func TestCreateSessionWithoutARepositoryClonesNothing(t *testing.T) {
	srv, _ := forgeServer(t)
	clone := watchClone(t, nil)

	if w := createSession(t, srv, `{}`, "doug@example.com"); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if clone.called {
		t.Error("a session with no repository cloned something")
	}
}

func listSessions(t *testing.T, srv *Server, who string) []any {
	t.Helper()
	w := get(t, srv, "/api/sessions", who)
	var body struct {
		Sessions []any `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Sessions
}

// A server with no workspace root has nowhere to put a repository. Cloning into
// a relative path would drop it into whatever directory dross was started in.
func TestCreateSessionWithNoWorkspaceRefusesARepository(t *testing.T) {
	forge.Reset()
	t.Cleanup(forge.Reset)
	tokens, err := NewForgeTokens(t.TempDir(), []byte("a cookie secret"))
	if err != nil {
		t.Fatal(err)
	}
	forge.Register(&fakeForge{name: "github", repos: []forge.Repo{
		{FullName: "doug/dross", CloneURL: "https://github.com/doug/dross.git"},
	}})
	_ = tokens.Save("doug@example.com", "github", forge.Token{Access: "gho_abc"})
	// No workdir: the manager has no per-session workspace to make.
	srv := New(storage.NewSessionStore(t.TempDir()),
		NewManager(&fakeProvider{}, nil, "m", ""), nil, testArts(t))
	srv.SetForges(tokens)
	clone := watchClone(t, nil)

	w := createSession(t, srv, `{"forge":"github","repo":"doug/dross"}`, "doug@example.com")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create: %d, want it refused", w.Code)
	}
	if clone.called {
		t.Error("cloned into a path with no workspace root")
	}
	if sessions := listSessions(t, srv, "doug@example.com"); len(sessions) != 0 {
		t.Errorf("a refused create left %d sessions behind", len(sessions))
	}
}
