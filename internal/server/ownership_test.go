package server

import (
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
