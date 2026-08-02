package agent

import (
	"encoding/json"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

// Approval must be an exact choice. A substring test treated any answer
// containing "allow" — including a denial that explains itself — as consent.
func TestApproveRequiresTheExactOption(t *testing.T) {
	call := provider.ToolCall{Name: "bash"}

	deny := []string{
		"Deny - that is not allowed",
		"not allowed",
		"Deny",
		"",
		"allow", // lowercase is not the offered option
		"Allow this once and never again, actually no",
	}
	for _, ans := range deny {
		a := &Agent{asker: func(AskRequest) (string, error) { return ans, nil }}
		if a.approve(call, false) {
			t.Errorf("answer %q was treated as approval", ans)
		}
	}

	a := &Agent{asker: func(AskRequest) (string, error) { return "Allow", nil }}
	if !a.approve(call, false) {
		t.Error("the Allow option was not honoured")
	}

	// "don't ask again" still whitelists for the session.
	b := &Agent{asker: func(AskRequest) (string, error) { return sessionAllowOpt, nil }}
	if !b.approve(call, true) || !b.sessionAllow["bash"] {
		t.Error("session-allow option not honoured")
	}
}

// With no asker there is nobody to ask, so a call that reached approve must be
// refused. Returning true instead made every "ask" rule a no-op for precisely
// the callers that cannot supervise the result — a server, a session pod, a
// relayed prompt.
func TestApproveWithoutAnAskerRefuses(t *testing.T) {
	a := &Agent{} // no asker
	if a.approve(provider.ToolCall{Name: "bash"}, false) {
		t.Error("approved a gated call with nobody to ask")
	}
	if a.approve(provider.ToolCall{Name: "write"}, true) {
		t.Error("approved a gated call with nobody to ask (session variant)")
	}
}

// The default mode must stay unattended-friendly: it never consults approve, so
// making approve refuse cannot break a server or a one-shot run that has not
// configured a gate.
func TestAutoModeNeverReachesApprove(t *testing.T) {
	a := &Agent{permMode: PermAuto, asker: func(AskRequest) (string, error) {
		t.Fatal("auto mode asked for approval")
		return "", nil
	}}
	for _, name := range []string{"write", "edit", "bash"} {
		if why, ok := a.gate(provider.ToolCall{Name: name, Input: json.RawMessage(`{"command":"ls"}`)}); !ok {
			t.Errorf("auto mode blocked %s: %s", name, why)
		}
	}
}
