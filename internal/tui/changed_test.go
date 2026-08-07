package tui

import (
	"strings"
	"testing"
)

// TestChangedFilesAccumulate: a file edited four times is one entry whose
// counts cover the whole session, which is what "what has this changed" asks.
func TestChangedFilesAccumulate(t *testing.T) {
	var m Model
	m.noteChange("pkg/agent/tools.go", 10, 2, false)
	m.noteChange("pkg/agent/tools.go", 5, 1, false)
	m.noteChange("README.md", 1, 0, false)

	if len(m.changed) != 2 {
		t.Fatalf("got %d entries, want 2 — the same file should fold into one", len(m.changed))
	}
	if got := m.changed[0]; got.path != "pkg/agent/tools.go" || got.added != 15 || got.removed != 3 {
		t.Errorf("first entry = %+v, want tools.go +15 -3", got)
	}
	// First-touch order, so the list does not reshuffle while being read.
	if m.changed[1].path != "README.md" {
		t.Errorf("order = %q, want README.md second", m.changed[1].path)
	}
}

// TestChangedSummaryTotals is what the bottom bar carries continuously.
func TestChangedSummaryTotals(t *testing.T) {
	var m Model
	if got := m.changedSummary(); got != "" {
		t.Errorf("an untouched session should say nothing, got %q", got)
	}
	m.noteChange("a.go", 10, 2, false)
	m.noteChange("b.go", 5, 0, true)

	got := stripANSI(m.changedSummary())
	for _, want := range []string{"2 files", "+15", "-2"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q missing %q", got, want)
		}
	}
}

// TestChangedSummaryOmitsAnEmptyDeletion keeps a fresh file from reading as
// "-0", which looks like a number that means something.
func TestChangedSummaryOmitsAnEmptyDeletion(t *testing.T) {
	var m Model
	m.noteChange("new.go", 12, 0, true)
	if got := stripANSI(m.changedSummary()); strings.Contains(got, "-0") {
		t.Errorf("summary should omit a zero deletion count: %q", got)
	}
}

// TestChangedIgnoresAnEmptyPath: an edit event without a file is not a file.
func TestChangedIgnoresAnEmptyPath(t *testing.T) {
	var m Model
	m.noteChange("", 1, 1, false)
	if len(m.changed) != 0 {
		t.Fatalf("an empty path should not become an entry: %+v", m.changed)
	}
}

// TestFilesCommandListsThem, and says so plainly when there is nothing.
func TestFilesCommandListsThem(t *testing.T) {
	m := New("dross", "m", "p", nil)
	m.width, m.height = 100, 40

	handled, _ := m.handleSlash("/files")
	if !handled {
		t.Fatal("/files should be a command")
	}
	if !strings.Contains(stripANSI(strings.Join(m.lines, "\n")), "nothing changed yet") {
		t.Fatalf("an untouched session should say so:\n%s", strings.Join(m.lines, "\n"))
	}

	m.lines = nil
	m.noteChange("pkg/agent/tools.go", 15, 3, false)
	m.noteChange("cmd/dross/main.go", 4, 0, true)
	if handled, _ := m.handleSlash("/files"); !handled {
		t.Fatal("/files should be a command")
	}
	out := stripANSI(strings.Join(m.lines, "\n"))
	for _, want := range []string{"pkg/agent/tools.go", "+15", "-3", "cmd/dross/main.go", "created"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing missing %q:\n%s", want, out)
		}
	}
}

// TestGuttersDistinguishSpeakers is the whole point of the rule: a glance down
// the left edge says who is talking, without reading a word.
func TestGuttersDistinguishSpeakers(t *testing.T) {
	// The colours, not the rendered strings: lipgloss drops ANSI when there is
	// no terminal, so every rule renders identically under `go test`.
	rebuildStyles()
	you, _ := gutterFor("you")
	phone, label := gutterFor("phone")
	if you.GetForeground() == phone.GetForeground() {
		t.Error("a prompt from a phone should not look like one you typed")
	}
	if label != "phone" {
		t.Errorf("a remote origin keeps its name, got %q", label)
	}
	if _, local := gutterFor("you"); local != "" {
		t.Errorf("a local turn needs no label, got %q", local)
	}
	if agentGutter.GetForeground() == you.GetForeground() {
		t.Error("the agent's rule should differ from yours")
	}
}

// TestUserTurnIsRuledOnEveryLine: a multi-line paste is one turn, so the rule
// runs its whole height rather than marking only the first line.
func TestUserTurnIsRuledOnEveryLine(t *testing.T) {
	m := New("dross", "m", "p", nil)
	m.width, m.height = 100, 40
	m.appendTurn(youGutter, "", "first line\nsecond line\nthird line")

	if len(m.lines) != 3 {
		t.Fatalf("got %d feed lines, want one per line of the turn", len(m.lines))
	}
	for i, l := range m.lines {
		if !strings.Contains(l, gutterMark) {
			t.Errorf("line %d has no rule: %q", i+1, stripANSI(l))
		}
	}
	if !strings.Contains(stripANSI(m.lines[2]), "third line") {
		t.Errorf("text lost: %q", stripANSI(m.lines[2]))
	}
}
