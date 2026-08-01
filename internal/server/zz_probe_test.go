package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/internal/auth"
	"github.com/dougkirkley/dross/internal/storage"
)

func TestProbeRoutes(t *testing.T) {
	a, err := auth.New(context.Background(), auth.Config{ServiceToken: "tok", ServiceIdentity: "s@x"})
	if err != nil {
		t.Fatal(err)
	}
	srv := testServer(t, a)
	paths := []string{
		"/api/health", "/api/sessions", "/api/whoami", "/",
		"/manifest.json", "/favicon.png", "/icons/x.png", "/icons/",
		"/icons", "/icons/../api/sessions", "/icons/..%2fapi/sessions",
		"/api/../api/sessions", "//api/sessions", "/api//sessions",
		"/a/deadbeef", "/s/x/y", "/auth/login", "/auth/logout",
		"/api/device/connect", "/api/health/../sessions",
		"/index.html", "/flutter_bootstrap.js", "/main.dart.js",
		"/./api/sessions", "/API/sessions", "/api/sessions/",
	}
	for _, p := range paths {
		r := httptest.NewRequest("GET", p, nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		body := w.Body.String()
		if len(body) > 60 {
			body = body[:60]
		}
		fmt.Printf("GET %-32s -> %d loc=%q body=%q\n", p, w.Code, w.Header().Get("Location"), strings.ReplaceAll(body, "\n", " "))
	}
	// OPTIONS pre-auth
	r := httptest.NewRequest("OPTIONS", "/api/sessions", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	fmt.Printf("OPTIONS /api/sessions -> %d ACAO=%q ACAC=%q\n", w.Code,
		w.Header().Get("Access-Control-Allow-Origin"), w.Header().Get("Access-Control-Allow-Credentials"))

	// CORS headers on an unauth 401
	r = httptest.NewRequest("GET", "/api/sessions", nil)
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	fmt.Printf("GET /api/sessions Origin:evil -> %d ACAO=%q\n", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
}

func TestProbeUploadTraversal(t *testing.T) {
	ws := t.TempDir()
	mgr := NewManager(&fakeProvider{}, nil, "m", ws)
	srv := New(storage.NewSessionStore(t.TempDir()), mgr, nil, testArts(t))

	for _, name := range []string{
		"../../evil.txt", "..", "../x", "a/../../b.txt", `..\..\win.txt`,
		"", ".", "/abs.txt", "....//x.txt", "\x00nul.txt",
	} {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		// bypass CreateFormFile escaping to send the raw filename
		h := make(map[string][]string)
		h["Content-Disposition"] = []string{`form-data; name="asset"; filename="` + strings.ReplaceAll(name, `"`, `\"`) + `"`}
		h["Content-Type"] = []string{"application/octet-stream"}
		fw, err := mw.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte("PWNED"))
		mw.Close()
		r := httptest.NewRequest("POST", "/api/upload", &b)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		fmt.Printf("upload %-18q -> %d %s\n", name, w.Code, strings.TrimSpace(w.Body.String()))
	}
	// Did anything escape ws/uploads?
	filepath.Walk(ws, func(p string, fi os.FileInfo, err error) error {
		fmt.Println("  fs:", strings.TrimPrefix(p, ws))
		return nil
	})
	parent := filepath.Dir(ws)
	if _, err := os.Stat(filepath.Join(parent, "evil.txt")); err == nil {
		t.Errorf("ESCAPED to %s", filepath.Join(parent, "evil.txt"))
	}
}

func TestProbeArtifactHeaders(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, `ev"il.html`), []byte(`<script>alert(1)</script>`), 0o644)
	os.WriteFile(filepath.Join(ws, "x.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), 0o644)
	mgr := NewManager(&fakeProvider{}, nil, "m", ws)
	srv := New(storage.NewSessionStore(t.TempDir()), mgr, nil, testArts(t))

	for _, f := range []string{`ev"il.html`, "x.svg"} {
		body, _ := json.Marshal(map[string]string{"path": f})
		w := do(t, srv, "POST", "/api/artifacts", string(body))
		var pub map[string]string
		json.Unmarshal(w.Body.Bytes(), &pub)
		fmt.Printf("publish %q -> %d mime=%q url=%q\n", f, w.Code, pub["mime"], pub["url"])
		if pub["url"] == "" {
			continue
		}
		g := do(t, srv, "GET", pub["url"], "")
		for _, hn := range []string{"Content-Type", "Content-Disposition", "X-Content-Type-Options", "Content-Security-Policy"} {
			fmt.Printf("   %s: %q\n", hn, g.Header().Get(hn))
		}
		// raw wire bytes
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", pub["url"], nil)
		srv.ServeHTTP(rec, req)
		res := rec.Result()
		var sb strings.Builder
		res.Header.Write(&sb)
		fmt.Printf("   WIRE:\n%s\n", sb.String())
		_ = http.StatusOK
	}
}

// Probe symlink escape via artifact publish.
func TestProbeSymlinkPublish(t *testing.T) {
	ws := t.TempDir()
	secret := filepath.Join(t.TempDir(), "id_rsa")
	os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600)
	if err := os.Symlink(secret, filepath.Join(ws, "link")); err != nil {
		t.Skip(err)
	}
	mgr := NewManager(&fakeProvider{}, nil, "m", ws)
	srv := New(storage.NewSessionStore(t.TempDir()), mgr, nil, testArts(t))
	w := do(t, srv, "POST", "/api/artifacts", `{"path":"link"}`)
	var pub map[string]string
	json.Unmarshal(w.Body.Bytes(), &pub)
	fmt.Printf("symlink publish -> %d url=%s\n", w.Code, pub["url"])
	if pub["url"] != "" {
		g := do(t, srv, "GET", pub["url"], "")
		fmt.Printf("   content=%q\n", g.Body.String())
	}
}

// Probe cross-session read: base workdir vs per-session sandbox.
func TestProbeCrossSession(t *testing.T) {
	ws := t.TempDir()
	mgr := NewManager(&fakeProvider{}, nil, "m", ws)
	srv := New(storage.NewSessionStore(t.TempDir()), mgr, nil, testArts(t))
	w := do(t, srv, "POST", "/api/sessions", `{}`)
	var c map[string]any
	json.Unmarshal(w.Body.Bytes(), &c)
	id := c["id"].(string)
	os.WriteFile(filepath.Join(ws, id, "secret.txt"), []byte("other session secret"), 0o644)
	w = do(t, srv, "POST", "/api/artifacts", `{"path":"`+id+`/secret.txt"}`)
	var pub map[string]string
	json.Unmarshal(w.Body.Bytes(), &pub)
	fmt.Printf("cross-session publish -> %d url=%s\n", w.Code, pub["url"])
}
