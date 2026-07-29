package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dougkirkley/ergon/pkg/agent"
)

// noRun fails if the agent is started (used to assert a keystroke did NOT begin
// a run).
func noRun(t *testing.T) RunFunc {
	return func(context.Context, string, string, func(agent.Event)) (*agent.Result, error) {
		t.Helper()
		t.Fatal("agent must not run")
		return nil, nil
	}
}

// TestSlashWhileRunning: a read-only slash command dispatches even while a run
// is in progress, without starting another run.
func TestSlashWhileRunning(t *testing.T) {
	m := New("ergon", "m", "openai", noRun(t))
	m.width, m.height = 80, 24
	m.layout()
	m.running = true // a run is in flight
	m.input.SetValue("/help")

	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = upd.(Model)
	if !m.helpOpen {
		t.Fatal("/help should open while running")
	}
	if !m.running {
		t.Fatal("running state must be unchanged by a read-only slash command")
	}
}

// TestClearBlockedWhileRunning: /clear is refused mid-run (it would wipe an
// active conversation).
func TestClearBlockedWhileRunning(t *testing.T) {
	m := New("ergon", "m", "openai", noRun(t))
	m.width, m.height = 80, 24
	m.layout()
	m.running = true
	m.lines = []string{"a", "b"}
	if _, _ = m.handleSlash("/clear"); len(m.lines) == 0 {
		t.Fatal("/clear must not wipe lines while running")
	}
}

// TestScrollSetsScrolledUp: pgup stops auto-follow; returning to the bottom
// resumes it.
func TestScrollSetsScrolledUp(t *testing.T) {
	m := New("ergon", "m", "openai", noRun(t))
	m.width, m.height = 80, 12
	m.layout()
	m.vp.SetContent(strings.Repeat("line\n", 100))
	m.vp.GotoBottom()
	if !m.vp.AtBottom() {
		t.Fatal("precondition: viewport should start at the bottom")
	}

	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = upd.(Model)
	if !m.scrolledUp {
		t.Fatal("pgup should set scrolledUp (stop following output)")
	}

	m.vp.GotoBottom()
	upd, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = upd.(Model)
	if m.scrolledUp {
		t.Fatal("scrolling back to the bottom should resume following")
	}
}

// TestSlashArgAutocomplete: after "/cmd ", argument values complete.
func TestSlashArgAutocomplete(t *testing.T) {
	m := New("ergon", "m", "openai", noRun(t))
	m.input.SetValue("/theme em")
	ms := m.matchSlash()
	if len(ms) != 1 || ms[0].name != "/theme ember" {
		t.Fatalf("expected [/theme ember], got %v", ms)
	}
	// /welcome completes its layouts.
	m.input.SetValue("/welcome ")
	if got := len(m.matchSlash()); got != len(welcomeNames) {
		t.Fatalf("/welcome should suggest %d layouts, got %d", len(welcomeNames), got)
	}
}

// TestBuiltinNotHijackedBySkill: a plugin skill named like a builtin cannot
// shadow the builtin command.
func TestBuiltinNotHijackedBySkill(t *testing.T) {
	m := New("ergon", "m", "openai", noRun(t))
	m.skillItems = []SkillItem{{Name: "help", UserInvocable: true, Body: "x"}}
	if _, ok := m.findSkill("/help"); ok {
		t.Fatal("/help must resolve to the builtin, not a skill")
	}
	// A non-colliding skill still resolves.
	m.skillItems = []SkillItem{{Name: "greet", UserInvocable: true, Body: "x"}}
	if _, ok := m.findSkill("/greet"); !ok {
		t.Fatal("/greet skill should resolve")
	}
}

// TestCtrlCInterrupts: the first ctrl+c cancels the running prompt (not exit);
// the model keeps running until the run's doneMsg arrives.
func TestCtrlCInterrupts(t *testing.T) {
	m := New("ergon", "m", "openai", noRun(t))
	m.width, m.height = 80, 24
	m.layout()
	canceled := false
	m.running = true
	m.runCancel = func() { canceled = true }

	upd, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = upd.(Model)
	if !canceled {
		t.Fatal("first ctrl+c should cancel the run")
	}
	if !m.running {
		t.Fatal("ctrl+c should not stop running before the run reports done")
	}
	if cmd != nil {
		t.Fatal("interrupt should not quit")
	}
}

// TestThemeSwitch: SetTheme changes the active theme; unknown names are rejected.
func TestThemeSwitch(t *testing.T) {
	defer SetTheme("crystal") // restore global state
	if !SetTheme("ember") || CurrentThemeName() != "ember" {
		t.Fatal("SetTheme(ember) should activate ember")
	}
	if SetTheme("nope") {
		t.Fatal("unknown theme should be rejected")
	}
}
