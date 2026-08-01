package agent

import (
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

// Plan mode promises read-only. An MCP server or plugin can write files and call
// APIs, so those tools must be gated like commands — otherwise the promise is
// false for exactly the configuration the server runs in.
func TestExternalToolsAreGatedLikeCommands(t *testing.T) {
	a := &Agent{
		permMode:   PermPlan,
		plugins:    map[string]PluginSpec{"deploy": {Name: "deploy"}},
		extraTools: map[string]extraTool{"gh__create_file": {}},
	}
	for _, name := range []string{"deploy", "gh__create_file", "bash", "write"} {
		if _, allowed := a.gate(provider.ToolCall{Name: name}); allowed {
			t.Errorf("plan mode allowed %q", name)
		}
	}
	// Genuinely read-only built-ins stay ungated.
	for _, name := range []string{"read", "shell_output", "ask_user"} {
		if _, allowed := a.gate(provider.ToolCall{Name: name}); !allowed {
			t.Errorf("plan mode blocked read-only tool %q", name)
		}
	}
}

// In accept-edits an external tool should prompt (it is command-class), not run
// silently.
func TestExternalToolsPromptInAcceptEdits(t *testing.T) {
	asked := 0
	a := &Agent{
		permMode:   PermAcceptEdits,
		extraTools: map[string]extraTool{"gh__create_file": {}},
		asker: func(AskRequest) (string, error) {
			asked++
			return "Deny", nil
		},
	}
	if _, allowed := a.gate(provider.ToolCall{Name: "gh__create_file"}); allowed {
		t.Error("MCP tool ran without approval")
	}
	if asked == 0 {
		t.Error("no approval was requested for an MCP tool")
	}
}
