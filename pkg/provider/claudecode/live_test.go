package claudecode

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dougkirkley/ergon/pkg/provider"
)

// TestLive drives the real claude CLI. Gated by HOLOCRON_LIVE=1 so it never runs
// in normal `go test`.
func TestLive(t *testing.T) {
	if os.Getenv("HOLOCRON_LIVE") != "1" {
		t.Skip("set HOLOCRON_LIVE=1 to run against the real claude CLI")
	}
	p, err := provider.New("claude-code", provider.Config{})
	if err != nil {
		t.Fatal(err)
	}
	s := p.NewSession(provider.SessionConfig{Model: "sonnet"})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	r, err := s.Send(ctx, provider.Input{Text: "Remember the number 42. Reply with just: ok"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("turn1: %q stop=%s in=%d out=%d", r.Text, r.StopReason, r.Usage.InputTokens, r.Usage.OutputTokens)

	r2, err := s.Send(ctx, provider.Input{Text: "What number did I ask you to remember? Reply with just the number."})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("turn2: %q", r2.Text)
	if !strings.Contains(r2.Text, "42") {
		t.Fatalf("session did not carry context; turn2=%q", r2.Text)
	}
}
