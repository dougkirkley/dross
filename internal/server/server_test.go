package server

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dougkirkley/ergon/internal/artifact"
	"github.com/dougkirkley/ergon/internal/auth"
	"github.com/dougkirkley/ergon/internal/provider"
	"github.com/dougkirkley/ergon/internal/storage"
)

func testArts(t *testing.T) *artifact.Store {
	t.Helper()
	s, err := artifact.NewStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// fakeProvider drives the agent loop to completion in one turn.
type fakeProvider struct{ text string }

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) NewSession(provider.SessionConfig) provider.Session {
	return &fakeSession{text: f.text}
}

type fakeSession struct {
	text string
	sent bool
}

func (s *fakeSession) Send(context.Context, provider.Input) (*provider.Response, error) {
	s.sent = true
	return &provider.Response{
		Text:       s.text,
		StopReason: provider.StopEndTurn,
		Usage:      provider.Usage{InputTokens: 10, OutputTokens: 5},
	}, nil
}

func testServer(t *testing.T, a *auth.Authenticator) *Server {
	t.Helper()
	store := storage.NewSessionStore(t.TempDir())
	mgr := NewManager(&fakeProvider{text: "hello from fake"}, nil, "test-model", t.TempDir())
	return New(store, mgr, a, testArts(t))
}

func do(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w
}

// waitIdle polls a session until it leaves "running".
func waitIdle(t *testing.T, srv *Server, id string) string {
	t.Helper()
	for i := 0; i < 100; i++ {
		w := do(t, srv, "GET", "/api/sessions/"+id, "")
		var v map[string]any
		json.Unmarshal(w.Body.Bytes(), &v)
		if s, _ := v["status"].(string); s != "running" {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("session never left running")
	return ""
}

// TestSessionLifecycle exercises create → prompt → status → events end to end.
func TestSessionLifecycle(t *testing.T) {
	srv := testServer(t, nil)

	// create
	w := do(t, srv, "POST", "/api/sessions", `{}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d", w.Code)
	}
	var created map[string]any
	json.Unmarshal(w.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	if !strings.HasPrefix(id, "ses_") {
		t.Fatalf("bad id %q", id)
	}

	// prompt (async)
	w = do(t, srv, "POST", "/api/sessions/"+id+"/prompt", `{"prompt":"hi"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("prompt: %d", w.Code)
	}
	if st := waitIdle(t, srv, id); st != "idle" {
		t.Fatalf("status = %q, want idle", st)
	}

	// events: prompt, text, done
	w = do(t, srv, "GET", "/api/sessions/"+id+"/events", "")
	var ev struct {
		Events []Event `json:"events"`
	}
	json.Unmarshal(w.Body.Bytes(), &ev)
	kinds := map[string]bool{}
	for _, e := range ev.Events {
		kinds[e.Kind] = true
	}
	for _, want := range []string{"prompt", "text", "done"} {
		if !kinds[want] {
			t.Fatalf("missing %q event; got %v", want, ev.Events)
		}
	}

	// ?after cursor drops earlier events
	last := ev.Events[len(ev.Events)-1].Seq
	w = do(t, srv, "GET", "/api/sessions/"+id+"/events?after="+itoa(last-1), "")
	json.Unmarshal(w.Body.Bytes(), &ev)
	if len(ev.Events) != 1 || ev.Events[0].Seq != last {
		t.Fatalf("after cursor: got %v", ev.Events)
	}
}

func TestDeleteSession(t *testing.T) {
	srv := testServer(t, nil)

	w := do(t, srv, "POST", "/api/sessions", `{}`)
	var created map[string]any
	json.Unmarshal(w.Body.Bytes(), &created)
	id, _ := created["id"].(string)

	if w := do(t, srv, "DELETE", "/api/sessions/"+id, ""); w.Code != http.StatusOK {
		t.Fatalf("delete: %d", w.Code)
	}
	// gone afterwards
	if w := do(t, srv, "GET", "/api/sessions/"+id, ""); w.Code != http.StatusNotFound {
		t.Fatalf("get after delete: want 404, got %d", w.Code)
	}
	// deleting again is a 404
	if w := do(t, srv, "DELETE", "/api/sessions/"+id, ""); w.Code != http.StatusNotFound {
		t.Fatalf("delete missing: want 404, got %d", w.Code)
	}
}

func TestPatchSessionModel(t *testing.T) {
	srv := testServer(t, nil)
	w := do(t, srv, "POST", "/api/sessions", `{}`)
	var created map[string]any
	json.Unmarshal(w.Body.Bytes(), &created)
	id, _ := created["id"].(string)

	w = do(t, srv, "PATCH", "/api/sessions/"+id, `{"model":"claude-haiku-4-5"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d", w.Code)
	}
	var updated map[string]any
	json.Unmarshal(w.Body.Bytes(), &updated)
	if updated["model"] != "claude-haiku-4-5" {
		t.Fatalf("model = %v, want claude-haiku-4-5", updated["model"])
	}
	// It persists on a subsequent GET.
	w = do(t, srv, "GET", "/api/sessions/"+id, "")
	json.Unmarshal(w.Body.Bytes(), &updated)
	if updated["model"] != "claude-haiku-4-5" {
		t.Fatalf("model not persisted: %v", updated["model"])
	}
}

func TestUnknownSession(t *testing.T) {
	srv := testServer(t, nil)
	for _, p := range []string{"/api/sessions/nope", "/api/sessions/nope/events"} {
		if w := do(t, srv, "GET", p, ""); w.Code != http.StatusNotFound {
			t.Fatalf("%s: want 404, got %d", p, w.Code)
		}
	}
	if w := do(t, srv, "POST", "/api/sessions/nope/prompt", `{"prompt":"x"}`); w.Code != http.StatusNotFound {
		t.Fatalf("prompt unknown: want 404, got %d", w.Code)
	}
}

func TestPromptValidation(t *testing.T) {
	srv := testServer(t, nil)
	w := do(t, srv, "POST", "/api/sessions", `{}`)
	var c map[string]any
	json.Unmarshal(w.Body.Bytes(), &c)
	id := c["id"].(string)
	if w := do(t, srv, "POST", "/api/sessions/"+id+"/prompt", `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("empty prompt: want 400, got %d", w.Code)
	}
}

// TestAuthWiring proves /api/health stays open while other /api routes are gated.
func TestAuthWiring(t *testing.T) {
	a, err := auth.New(context.Background(), auth.Config{StaticToken: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	srv := testServer(t, a)

	if w := do(t, srv, "GET", "/api/health", ""); w.Code != http.StatusOK {
		t.Fatalf("health should be open: %d", w.Code)
	}
	if w := do(t, srv, "GET", "/api/sessions", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sessions no-auth: want 401, got %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/api/sessions", nil)
	r.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("sessions with token: want 200, got %d", w.Code)
	}
}

// TestArtifacts covers publish (workspace file → capability URL) and the
// unauthenticated GET that serves it.
func TestArtifacts(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "shot.png"), []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := storage.NewSessionStore(t.TempDir())
	mgr := NewManager(&fakeProvider{}, nil, "m", ws)
	srv := New(store, mgr, nil, testArts(t))

	// Publish.
	w := do(t, srv, "POST", "/api/artifacts", `{"path":"shot.png"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("publish: %d", w.Code)
	}
	var pub map[string]string
	json.Unmarshal(w.Body.Bytes(), &pub)
	if pub["mime"] != "image/png" || !strings.HasPrefix(pub["url"], "/a/") {
		t.Fatalf("publish resp = %v", pub)
	}

	// Fetch the capability URL — unauth, correct bytes and type.
	w = do(t, srv, "GET", pub["url"], "")
	if w.Code != http.StatusOK || w.Body.String() != "\x89PNG\r\n\x1a\nfake" {
		t.Fatalf("get: code=%d body=%q", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type = %q", ct)
	}

	// Traversal is rejected.
	if w := do(t, srv, "POST", "/api/artifacts", `{"path":"../../etc/passwd"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("traversal: want 400, got %d", w.Code)
	}
	// Bad id → 404.
	if w := do(t, srv, "GET", "/a/deadbeef", ""); w.Code != http.StatusNotFound {
		t.Fatalf("bad id: want 404, got %d", w.Code)
	}
}

// TestUploadIngest writes an uploaded file into the workspace.
func TestUploadIngest(t *testing.T) {
	ws := t.TempDir()
	store := storage.NewSessionStore(t.TempDir())
	mgr := NewManager(&fakeProvider{}, nil, "m", ws)
	srv := New(store, mgr, nil, testArts(t))

	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("asset", "notes.txt")
	fw.Write([]byte("hi"))
	mw.Close()

	r := httptest.NewRequest("POST", "/api/upload", &b)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("upload: %d", w.Code)
	}
	got, err := os.ReadFile(filepath.Join(ws, "uploads", "notes.txt"))
	if err != nil || string(got) != "hi" {
		t.Fatalf("ingested file: %q %v", got, err)
	}
}

// TestSessionEventOrdering unit-tests seq assignment and history filtering.
func TestSessionEventOrdering(t *testing.T) {
	s := &Session{ID: "s", status: "idle"}
	for i := 0; i < 5; i++ {
		s.emit("k", "d")
	}
	all := s.history(0)
	if len(all) != 5 {
		t.Fatalf("history len = %d", len(all))
	}
	for i, e := range all {
		if e.Seq != i+1 {
			t.Fatalf("seq[%d] = %d", i, e.Seq)
		}
	}
	if got := s.history(3); len(got) != 2 || got[0].Seq != 4 {
		t.Fatalf("history(3) = %v", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
