package server

import (
	"strings"
	"testing"

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
		if e.Kind == "ask" {
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
