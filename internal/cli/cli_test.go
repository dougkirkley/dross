package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootHasSubcommands(t *testing.T) {
	root := NewRootCmd()
	want := map[string]bool{"agent": false, "serve": false, "tui": false, "brief": false, "bench": false}
	for _, c := range root.Commands() {
		want[c.Name()] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("root missing subcommand %q", name)
		}
	}
}

func TestAgentRequiresPrompt(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"agent"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	if err := root.Execute(); err == nil {
		t.Fatal("agent with no prompt should error")
	}
}

// TestTUIRenderIsHermetic renders the demo welcome view — no model, no network —
// proving the render path and exercising the tui command wiring end to end.
func TestTUIRenderIsHermetic(t *testing.T) {
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"tui", "--render", "--welcome", "--width", "80", "--height", "22"})
	if err := root.Execute(); err != nil {
		t.Fatalf("tui --render: %v", err)
	}
	if !strings.Contains(out.String(), "cost-aware") {
		t.Fatalf("render missing tagline:\n%s", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"nope"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	if err := root.Execute(); err == nil {
		t.Fatal("unknown command should error")
	}
}

// TestFlagsParse checks a representative command binds its flags without error.
func TestFlagsParse(t *testing.T) {
	cmd := newServeCmd()
	if err := cmd.ParseFlags([]string{"--addr", ":9999", "--auth-token", "x", "--model", "m"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, _ := cmd.Flags().GetString("addr"); got != ":9999" {
		t.Fatalf("addr = %q", got)
	}
}
