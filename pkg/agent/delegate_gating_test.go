package agent

import (
	"encoding/json"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

// Delegation must not be a way around the lead's permissions. Plan mode is
// documented as read-only, so a worker spawned from it must refuse bash too.
func TestWorkerInheritsLeadGating(t *testing.T) {
	lead := &Agent{
		permMode:   PermPlan,
		allowTools: map[string]bool{"read": true, "delegate": true},
		permRules:  map[string]permRule{"bash": {blanket: ToolDeny}},
	}
	sub := &Agent{}
	withInheritedGating(lead)(sub)

	if sub.permMode != PermPlan {
		t.Errorf("permMode = %q; want plan", sub.permMode)
	}
	if !sub.allowTools["read"] || sub.allowTools["bash"] {
		t.Errorf("allowTools not inherited: %v", sub.allowTools)
	}
	if r, ok := sub.permRules["bash"]; !ok || r.blanket != ToolDeny {
		t.Errorf("permRules not inherited: %v", sub.permRules)
	}

	// The worker actually refuses the call, not just holds the config.
	call := provider.ToolCall{Name: "bash", Input: json.RawMessage(`{"command":"touch /tmp/pwn"}`)}
	if _, allowed := sub.gate(call); allowed {
		t.Error("worker allowed bash despite inheriting plan mode and a deny rule")
	}
}

// Copies must be independent: a worker must not be able to widen the lead's
// permissions by mutating shared maps.
func TestInheritedGatingIsACopy(t *testing.T) {
	lead := &Agent{
		allowTools: map[string]bool{"read": true},
		permRules:  map[string]permRule{"bash": {blanket: ToolDeny}},
	}
	sub := &Agent{}
	withInheritedGating(lead)(sub)

	sub.allowTools["bash"] = true
	sub.permRules["bash"] = permRule{blanket: ToolAllow}

	if lead.allowTools["bash"] {
		t.Error("worker mutated the lead's tool filter")
	}
	if lead.permRules["bash"].blanket != ToolDeny {
		t.Error("worker mutated the lead's permission rules")
	}
}
