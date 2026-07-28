package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dougkirkley/holocron/internal/agent"
)

// TestBangShellEscape verifies "!cmd" runs a shell command instead of prompting
// the agent, and its output lands in the log.
func TestBangShellEscape(t *testing.T) {
	run := func(context.Context, string, func(agent.Event)) (*agent.Result, error) {
		t.Fatal("agent must not run for a ! command")
		return nil, nil
	}
	m := New("holocron", "m", "openai", run)
	m.width, m.height = 80, 24
	m.layout()
	m.input.SetValue("!echo bang_marker_42")

	upd, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = upd.(Model)
	if cmd == nil {
		t.Fatal("expected a command from ! escape")
	}
	msg := cmd() // run the shell
	upd, _ = m.Update(msg)
	m = upd.(Model)

	joined := strings.Join(m.lines, "\n")
	if !strings.Contains(joined, "bang_marker_42") {
		t.Fatalf("shell output missing; log=\n%s", joined)
	}
}
