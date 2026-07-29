package claudecode

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/dougkirkley/ergon/pkg/provider"
)

func TestDrivenLive(t *testing.T) {
	if os.Getenv("ERGON_LIVE") != "1" {
		t.Skip("set ERGON_LIVE=1")
	}
	p, _ := provider.New("claude-code", provider.Config{})
	s := p.NewSession(provider.SessionConfig{
		Model:  "sonnet",
		System: "You are ergon, a coding agent.",
		Tools: []provider.ToolDef{{
			Name:        "read",
			Description: "Read a file from disk and return its contents.",
			Properties:  map[string]any{"path": map[string]any{"type": "string", "description": "file path"}},
			Required:    []string{"path"},
		}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	r, err := s.Send(ctx, provider.Input{Text: "Read go.mod and tell me the module path."})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("stop=%s text=%q calls=%d", r.StopReason, r.Text, len(r.ToolCalls))
	for _, c := range r.ToolCalls {
		t.Logf("  call %s %s", c.Name, string(c.Input))
	}
}
