package session

import (
	"testing"

	"github.com/dougkirkley/dross/internal/storage"
)

func TestBriefRoundTrip(t *testing.T) {
	store := storage.NewSessionStore(t.TempDir())

	// Missing → found=false, no error.
	if _, ok, err := Load(store, "nope"); err != nil || ok {
		t.Fatalf("missing: ok=%v err=%v", ok, err)
	}

	if err := Save(store, "demo", "# brief\nstate: done"); err != nil {
		t.Fatal(err)
	}
	b, ok, err := Load(store, "demo")
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if b.Name != "demo" || b.Content != "# brief\nstate: done" {
		t.Fatalf("brief = %+v", b)
	}
	if b.Updated.IsZero() {
		t.Fatal("Updated not set")
	}

	// Overwrite.
	if err := Save(store, "demo", "v2"); err != nil {
		t.Fatal(err)
	}
	b, _, _ = Load(store, "demo")
	if b.Content != "v2" {
		t.Fatalf("overwrite failed: %q", b.Content)
	}

	// List.
	Save(store, "other", "x")
	names, err := List(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Fatalf("list = %v", names)
	}
}

// TestBrief_NameTraversal ensures a session name can't escape the store dir.
func TestBrief_NameTraversal(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewSessionStore(dir)
	// filepath.Base in the store strips path separators, so this writes
	// "passwd.json" inside dir, not outside it.
	if err := Save(store, "../../passwd", "x"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := Load(store, "../../passwd"); !ok {
		t.Fatal("expected the sanitized name to round-trip")
	}
}
