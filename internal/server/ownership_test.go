package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dougkirkley/dross/internal/storage"
)

// Sessions carry prompts, workspace contents and agent output, so one identity
// must never see or touch another's — including the machine credential, whose
// token lives in a k8s secret.
func TestSessionsAreScopedToTheirOwner(t *testing.T) {
	_ = storage.NewSessionStore(t.TempDir())
	m := NewManager(&fakeProvider{}, nil, "m", t.TempDir())

	mine := m.create(CreateReq{}, "doug@example.com")
	theirs := m.create(CreateReq{}, "someone@else.com")

	// List is filtered.
	if got := m.list("doug@example.com"); len(got) != 1 || got[0].ID != mine.ID {
		t.Fatalf("list(doug) = %d sessions; want only mine", len(got))
	}
	// Direct id access is refused, and indistinguishable from a missing session.
	if _, ok := m.get(theirs.ID, "doug@example.com"); ok {
		t.Error("read another identity's session")
	}
	// Delete is refused too, and must not actually remove it.
	if m.remove(theirs.ID, "doug@example.com") {
		t.Error("deleted another identity's session")
	}
	if _, ok := m.get(theirs.ID, "someone@else.com"); !ok {
		t.Error("owner lost their session to a foreign delete attempt")
	}
	// The service identity is not a superuser.
	if _, ok := m.get(mine.ID, "service@dross"); ok {
		t.Error("service identity read a user session")
	}
	// Owner still reaches their own.
	if _, ok := m.get(mine.ID, "doug@example.com"); !ok {
		t.Error("owner cannot read own session")
	}
}

// With auth off there is no identity, so nothing should be hidden.
func TestUnauthenticatedServerSharesSessions(t *testing.T) {
	m := NewManager(&fakeProvider{}, nil, "m", t.TempDir())
	s := m.create(CreateReq{}, "")
	if _, ok := m.get(s.ID, ""); !ok {
		t.Error("open-mode session not reachable")
	}
	if got := m.list(""); len(got) != 1 {
		t.Errorf("list = %d; want 1", len(got))
	}
}

// Deleting a session must take its workspace with it: those files are the
// user's data, and on a persistent volume an orphaned sandbox would linger
// forever and eventually fill the disk.
func TestDeleteRemovesTheWorkspace(t *testing.T) {
	base := t.TempDir()
	m := NewManager(&fakeProvider{}, nil, "m", base)
	s := m.create(CreateReq{}, "doug@example.com")

	if s.workdir == "" || s.workdir == base {
		t.Fatalf("expected a per-session workdir, got %q", s.workdir)
	}
	if err := os.WriteFile(filepath.Join(s.workdir, "secret.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !m.remove(s.ID, "doug@example.com") {
		t.Fatal("remove failed")
	}
	if _, err := os.Stat(s.workdir); !os.IsNotExist(err) {
		t.Errorf("workspace %s still exists after delete (err=%v)", s.workdir, err)
	}
	// The shared base must survive — only the session's own subdir goes.
	if _, err := os.Stat(base); err != nil {
		t.Errorf("base workdir was removed: %v", err)
	}
}

// A refused delete must not touch the owner's files.
func TestRefusedDeleteKeepsTheWorkspace(t *testing.T) {
	base := t.TempDir()
	m := NewManager(&fakeProvider{}, nil, "m", base)
	s := m.create(CreateReq{}, "owner@example.com")

	if m.remove(s.ID, "intruder@example.com") {
		t.Fatal("foreign delete succeeded")
	}
	if _, err := os.Stat(s.workdir); err != nil {
		t.Errorf("workspace destroyed by a refused delete: %v", err)
	}
}
