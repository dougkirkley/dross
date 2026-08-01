package agent

import (
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
