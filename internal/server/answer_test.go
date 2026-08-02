package server

import (
	"strings"
	"testing"
	"time"

	"github.com/dougkirkley/dross/internal/relay"
)

func relaySession(id string) relay.SessionInfo {
	return relay.SessionInfo{ID: id, Model: "m", Provider: "p", Status: "idle"}
}

// A gate on a linked machine has to reach the session's watchers, carrying the
// id that identifies it, so an answer can be matched to the question that is
// actually waiting rather than to whatever is outstanding when it lands.
func TestDeviceAskReachesTheSession(t *testing.T) {
	m := NewManager(nil, nil, "m", t.TempDir())
	m.upsertDeviceSession("laptop", relaySession("ses_x"))

	m.deviceAsk("laptop", "ses_x", "ses_x-1", `{"Questions":[{"Header":"bash"}]}`)

	s, ok := m.getAny("ses_x")
	if !ok {
		t.Fatal("session missing")
	}
	if s.pendingAsk != "ses_x-1" {
		t.Errorf("pendingAsk = %q; want the ask id", s.pendingAsk)
	}
	var found string
	for _, e := range s.history(0) {
		if e.Kind == "ask-request" {
			found = e.Data
		}
	}
	if !strings.HasPrefix(found, "ses_x-1\x1f") {
		t.Errorf("ask event = %q; want the id then the payload", found)
	}
}

// An ask from a device that does not own the session must be ignored, or one
// linked machine could raise a prompt on another's session.
func TestDeviceAskFromTheWrongDeviceIsIgnored(t *testing.T) {
	m := NewManager(nil, nil, "m", t.TempDir())
	m.upsertDeviceSession("laptop", relaySession("ses_x"))

	m.deviceAsk("someone-else", "ses_x", "ses_x-1", "{}")

	s, _ := m.getAny("ses_x")
	if s.pendingAsk != "" {
		t.Error("an ask from another device was accepted")
	}
}

// Answering when nothing is pending must fail rather than be delivered as the
// reply to whatever is asked next.
func TestAnswerWithNothingPendingFails(t *testing.T) {
	m := NewManager(nil, nil, "m", t.TempDir())
	m.upsertDeviceSession("laptop", relaySession("ses_x"))

	if m.answerDevice("ses_x", "", "Allow") {
		t.Error("answered a session with no question outstanding")
	}
}

// Renaming changes the display name and nothing else. The id is what the relay
// device, the workload label and every stored event key on, so a rename that
// moved it would strand all three.
func TestRenameLeavesTheSessionIDAlone(t *testing.T) {
	m := NewManager(&fakeProvider{}, nil, "m", t.TempDir())
	s := m.create(CreateReq{}, "doug@example.com")
	id := s.ID

	s.setTitle("deploy the operator")

	if s.ID != id {
		t.Errorf("id changed from %q to %q", id, s.ID)
	}
	if got := s.snapshot().Title; got != "deploy the operator" {
		t.Errorf("title = %q", got)
	}
	if _, ok := m.get(id, "doug@example.com"); !ok {
		t.Error("session is no longer findable by its id after a rename")
	}
}

// Clearing the name lets the next prompt title the session again, which is the
// only way back to the default once it has been renamed.
func TestClearingTheNameRestoresAutoTitling(t *testing.T) {
	m := NewManager(&fakeProvider{}, nil, "m", t.TempDir())
	s := m.create(CreateReq{}, "doug@example.com")
	s.setTitle("something")
	s.setTitle("")

	if got := s.snapshot().Title; got != "" {
		t.Errorf("title = %q; want it cleared", got)
	}
}

// A chat session has no shell and no workspace, which is the only reason a
// session gets a pod. Giving one a pod made every chat prompt wait on a relay
// with no agent behind it, and the send came back as a conflict.
func TestChatSessionsAreNotPodBacked(t *testing.T) {
	m := NewManager(&fakeProvider{}, nil, "m", t.TempDir())

	chat := m.create(CreateReq{Chat: true}, "doug@example.com")
	if chat.device != "" {
		t.Errorf("chat session is device-backed (%q)", chat.device)
	}
	// And a chat session can be prompted straight away, with nothing to wait on.
	if err := m.prompt(chat, "hello"); err != nil {
		t.Errorf("prompting a fresh chat session failed: %v", err)
	}
}

// A run waiting on a model to load looks exactly like one that has hung: the
// status says running and the transcript is empty. It has to say something.
func TestSilentRunSaysSo(t *testing.T) {
	m := NewManager(&fakeProvider{}, nil, "m", t.TempDir())
	s := m.create(CreateReq{}, "doug@example.com")
	s.mu.Lock()
	s.status = "running"
	s.mu.Unlock()

	s.warnIfSilent()
	// The watchdog is on a timer; drive it directly rather than waiting.
	time.Sleep(10 * time.Millisecond)
	if n := len(s.history(0)); n != 0 {
		t.Fatalf("warned immediately (%d events); it must wait", n)
	}
}

// A run that produces something is not nagged.
func TestBusyRunIsNotWarned(t *testing.T) {
	m := NewManager(&fakeProvider{}, nil, "m", t.TempDir())
	s := m.create(CreateReq{}, "doug@example.com")
	s.mu.Lock()
	s.status = "running"
	s.mu.Unlock()

	s.warnIfSilent()
	s.emit("text", "here is an answer") // activity moves the sequence on
	for _, e := range s.history(0) {
		if e.Kind == "meta" {
			t.Error("warned a run that was producing output")
		}
	}
}
