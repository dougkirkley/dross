package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/internal/storage"
)

// Publishing must not follow a symlink out of the workspace. A reviewer used
// exactly this to turn a planted link into a private key served at a URL.
func TestPublishCannotFollowSymlinkOutOfWorkspace(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Symlink(secret, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"link", "dir/id_rsa", "../id_rsa", secret} {
		if got, err := resolveInside(root, p); err == nil {
			t.Errorf("resolveInside(%q) allowed escape -> %s", p, got)
		}
	}

	// A real file in the workspace still publishes.
	if err := os.WriteFile(filepath.Join(root, "shot.png"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveInside(root, "shot.png"); err != nil {
		t.Errorf("legitimate file rejected: %v", err)
	}
}

// Revocation has to mean something: a shared artifact must not be cached so
// long that unsharing leaves an intermediary still serving it.
func TestSharedArtifactsAreNotCachedImmutably(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "shot.png"), []byte("\x89PNG\r\n\x1a\nx"), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(&fakeProvider{}, nil, "m", ws)
	srv := New(storage.NewSessionStore(t.TempDir()), mgr, nil, testArts(t))

	w := do(t, srv, "POST", "/api/artifacts", `{"path":"shot.png"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("publish: %d", w.Code)
	}
	var pub map[string]string
	json.Unmarshal(w.Body.Bytes(), &pub)

	w = do(t, srv, "POST", "/api/artifacts/"+pub["id"]+"/share", "")
	if w.Code != http.StatusOK {
		t.Fatalf("share: %d", w.Code)
	}
	var sh map[string]string
	json.Unmarshal(w.Body.Bytes(), &sh)

	w = do(t, srv, "GET", sh["url"], "")
	cc := w.Header().Get("Cache-Control")
	if strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q; immutable defeats unshare", cc)
	}
	if !strings.Contains(cc, "max-age=300") {
		t.Errorf("Cache-Control = %q; want a short shared-cache TTL", cc)
	}

	// And revoking really stops serving it at the origin.
	if w := do(t, srv, "DELETE", "/api/artifacts/"+pub["id"]+"/share", ""); w.Code != http.StatusOK {
		t.Fatalf("unshare: %d", w.Code)
	}
	if w := do(t, srv, "GET", sh["url"], ""); w.Code != http.StatusNotFound {
		t.Errorf("revoked link still served: %d", w.Code)
	}
}
