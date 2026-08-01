package server

import (
	"testing"

	"github.com/dougkirkley/dross/internal/storage"
)

// A deploy must not discard the user's work: history, cost and ownership all
// have to come back.
func TestSessionsSurviveARestart(t *testing.T) {
	dir := t.TempDir()

	m := NewManager(&fakeProvider{}, nil, "m", t.TempDir())
	m.SetStore(storage.NewSessionStore(dir))
	s := m.create(CreateReq{Model: "gpt"}, "doug@example.com")
	s.emit("prompt", "hello")
	s.emit("text", "world")
	s.addCost(0.25)
	m.flush()

	// A fresh process over the same directory.
	m2 := NewManager(&fakeProvider{}, nil, "m", t.TempDir())
	m2.SetStore(storage.NewSessionStore(dir))
	n, err := m2.Restore()
	if err != nil || n != 1 {
		t.Fatalf("Restore = %d, %v; want 1", n, err)
	}
	got, ok := m2.get(s.ID, "doug@example.com")
	if !ok {
		t.Fatal("restored session not reachable by its owner")
	}
	if got.Model != "gpt" {
		t.Errorf("model = %q", got.Model)
	}
	if ev := got.history(0); len(ev) != 2 || ev[0].Data != "hello" {
		t.Errorf("history = %+v; want the 2 emitted events", ev)
	}
	if v := got.snapshot(); v.Cost != 0.25 {
		t.Errorf("cost = %v; want 0.25", v.Cost)
	}
	// Ownership survives, so a restart can't leak sessions between identities.
	if _, ok := m2.get(s.ID, "someone@else.com"); ok {
		t.Error("restored session visible to another identity")
	}
	// A session that was running is idle again — the run did not survive.
	if v := got.snapshot(); v.Status != "idle" {
		t.Errorf("status = %q; want idle", v.Status)
	}
}

// Deleting must be permanent, not undone by the next boot.
func TestDeletedSessionsStayDeleted(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(&fakeProvider{}, nil, "m", t.TempDir())
	m.SetStore(storage.NewSessionStore(dir))
	s := m.create(CreateReq{}, "doug@example.com")
	s.emit("prompt", "x")
	m.flush()
	if !m.remove(s.ID, "doug@example.com") {
		t.Fatal("remove failed")
	}

	m2 := NewManager(&fakeProvider{}, nil, "m", t.TempDir())
	m2.SetStore(storage.NewSessionStore(dir))
	if n, _ := m2.Restore(); n != 0 {
		t.Errorf("restored %d sessions; a deleted one came back", n)
	}
}
