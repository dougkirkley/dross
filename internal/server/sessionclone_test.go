package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/internal/forge"
	"github.com/dougkirkley/dross/internal/relay"
	"github.com/dougkirkley/dross/internal/storage"
)

// cloneCall is what setting a workspace up was asked to do, captured instead of
// run.
type cloneCall struct {
	dir, url, branch string
	called           bool
	// work is the branch the session was cut onto afterwards.
	work string
}

// watchClone replaces the real clone and branch for one test.
func watchClone(t *testing.T, err error) *cloneCall {
	t.Helper()
	got := &cloneCall{}
	realClone, realBranch := cloneRepo, branchRepo
	cloneRepo = func(_ context.Context, dir, url, branch string) error {
		got.dir, got.url, got.branch, got.called = dir, url, branch, true
		return err
	}
	branchRepo = func(_ context.Context, _, name string) error {
		got.work = name
		return nil
	}
	t.Cleanup(func() { cloneRepo, branchRepo = realClone, realBranch })
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
	if filepath.Base(clone.dir) != "dross" {
		t.Errorf("cloned into %q, want a directory named for the repository", clone.dir)
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

// Cloning beside the workspace is only half of it: the session has to run in
// the clone, or every prompt starts by working out where the code went.
func TestCreateSessionRunsInsideTheClone(t *testing.T) {
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
	var snap map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	sess, ok := srv.mgr.get(snap["id"].(string), "doug@example.com")
	if !ok {
		t.Fatal("the session is gone")
	}
	if sess.workdir != clone.dir {
		t.Errorf("session runs in %q, cloned into %q", sess.workdir, clone.dir)
	}
}

// Deleting takes the session's own directory, not just the clone inside it.
// Removing only what it ran in would leave the parent behind for ever.
func TestDeleteRemovesTheWholeSessionDirectory(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(&fakeForge{name: "github", repos: []forge.Repo{
		{FullName: "doug/dross", CloneURL: "https://github.com/doug/dross.git"},
	}})
	_ = tokens.Save("doug@example.com", "github", forge.Token{Access: "gho_abc"})
	clone := watchClone(t, nil)
	// The clone is watched, not run, so make the directory it would have made.
	base := srv.mgr.workdir

	w := createSession(t, srv, `{"forge":"github","repo":"doug/dross"}`, "doug@example.com")
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var snap map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &snap)
	id := snap["id"].(string)
	if err := os.MkdirAll(clone.dir, 0o700); err != nil {
		t.Fatal(err)
	}

	if !srv.mgr.remove(id, "doug@example.com") {
		t.Fatal("delete refused")
	}
	if _, err := os.Stat(filepath.Join(base, id)); !os.IsNotExist(err) {
		t.Errorf("the session directory survived deletion: %v", err)
	}
}

// A session works on a branch of its own. Committing straight onto the branch
// it was cut from is the one outcome nobody asks for.
func TestCreateSessionCutsItsOwnBranch(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(&fakeForge{name: "github", repos: []forge.Repo{
		{FullName: "doug/dross", CloneURL: "https://github.com/doug/dross.git", Branch: "main"},
	}})
	_ = tokens.Save("doug@example.com", "github", forge.Token{Access: "gho_abc"})
	clone := watchClone(t, nil)

	if w := createSession(t, srv, `{"forge":"github","repo":"doug/dross"}`, "doug@example.com"); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	// Cut from the repository's default, not instead of it.
	if clone.branch != "main" {
		t.Errorf("cloned branch %q", clone.branch)
	}
	if !strings.HasPrefix(clone.work, "dross/") || clone.work == "dross/" {
		t.Errorf("working branch %q, want one named for the session", clone.work)
	}
}

// The transcript is where somebody looks to find out whether anything is
// happening. A session that spends its first half minute cloning in silence
// reads as one that is broken.
func TestCreateSessionSaysItIsCloning(t *testing.T) {
	srv, tokens := forgeServer(t)
	forge.Register(&fakeForge{name: "github", repos: []forge.Repo{
		{FullName: "doug/dross", CloneURL: "https://github.com/doug/dross.git", Branch: "main"},
	}})
	_ = tokens.Save("doug@example.com", "github", forge.Token{Access: "gho_abc"})
	watchClone(t, nil)

	w := createSession(t, srv, `{"forge":"github","repo":"doug/dross"}`, "doug@example.com")
	var snap map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &snap)
	sess, ok := srv.mgr.get(snap["id"].(string), "doug@example.com")
	if !ok {
		t.Fatal("the session is gone")
	}

	var said []string
	for _, e := range sess.history(0) {
		if e.Kind == "meta" {
			said = append(said, e.Data)
		}
	}
	joined := strings.Join(said, " | ")
	// git's own words, so somebody watching a session start is reading
	// something they already know how to read.
	if !strings.Contains(joined, "Cloning into 'dross'...") {
		t.Errorf("the session never said it was cloning: %q", joined)
	}
	// In this process the clone is done by the time the create returns, so the
	// finish is reported here rather than waiting on an agent to attach.
	if !strings.Contains(joined, "Switched to a new branch 'dross/") {
		t.Errorf("the session never said it had finished: %q", joined)
	}
}

func TestWorkBranch(t *testing.T) {
	for _, c := range []struct{ session, want string }{
		{"patient-ridge", "dross/patient-ridge"},
		{"Dusky_Quarry", "dross/dusky-quarry"},
		// A ref name git will actually accept, whatever it was made from.
		{"", "dross/session"},
		{"...", "dross/session"},
		{"a b/c", "dross/abc"},
	} {
		if got := WorkBranch(c.session); got != c.want {
			t.Errorf("WorkBranch(%q) = %q, want %q", c.session, got, c.want)
		}
	}
}

// A pod clones on its way up, so the only honest moment to call the workspace
// ready is when the agent turns up. Until then "cloning" is the truth.
func TestSetupFinishesWhenTheAgentAttaches(t *testing.T) {
	srv, _ := forgeServer(t)
	sess := srv.mgr.create(CreateReq{}, "doug@example.com")
	sess.markDevice(sess.ID)
	sess.beginSetup("cloning doug/dross…", "cloned doug/dross, working on dross/x")

	meta := func() string {
		var out []string
		for _, e := range sess.history(0) {
			if e.Kind == "meta" {
				out = append(out, e.Data)
			}
		}
		return strings.Join(out, " | ")
	}
	if strings.Contains(meta(), "cloned") {
		t.Fatalf("called it finished before the agent attached: %q", meta())
	}

	// The way a pod actually turns up: it announces the session it is running.
	// Driving anything else here is how this passed while production never ran
	// the path at all.
	attach := func() {
		srv.mgr.upsertDeviceSession(sess.ID, relay.SessionInfo{ID: sess.ID})
	}
	attach()
	if !strings.Contains(meta(), "cloned doug/dross") {
		t.Errorf("the agent attaching did not report the setup: %q", meta())
	}

	// Once. A pod that reconnects after a drop has not set anything up again.
	before := meta()
	srv.mgr.setDeviceConnected(sess.ID, false)
	attach()
	if meta() != before {
		t.Errorf("reconnecting reported the setup again: %q", meta())
	}
}
