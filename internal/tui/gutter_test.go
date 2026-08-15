package tui

import (
	"strings"
	"testing"
)

// TestOnlyTheFocusedBlockWearsTheBar is the change.
//
// The transcript baked a gutter into every line, so every message wore one at
// once and the eye had nothing to follow. One block carries it now, and it
// follows the newest thing said.
func TestOnlyTheFocusedBlockWearsTheBar(t *testing.T) {
	m := New("dross", "m", "p", nil)
	m.appendTurn(youGutter, "", "first question")
	m.appendTurn(youGutter, "", "second question")

	body := m.body()
	lines := turnLines(m)
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), body)
	}
	if strings.Contains(lines[0], gutterMark) {
		t.Errorf("the older block still wears a bar: %q", lines[0])
	}
	if !strings.Contains(lines[1], gutterMark) {
		t.Errorf("the newest block should wear the bar: %q", lines[1])
	}
}

// Blanking the mark rather than removing it keeps the text aligned; otherwise
// every unfocused line shifts left and the transcript ripples on every click.
func TestUnfocusedLinesKeepTheirAlignment(t *testing.T) {
	m := New("dross", "m", "p", nil)
	m.appendTurn(youGutter, "", "older")
	m.appendTurn(youGutter, "", "newer")

	lines := turnLines(m)
	a := len([]rune(stripANSI(lines[0])))
	b := len([]rune(stripANSI(lines[1])))
	if a != b {
		t.Fatalf("alignment drifted: unfocused %d cols, focused %d cols\n%q\n%q", a, b, lines[0], lines[1])
	}
}

// TestClickingAnOlderBlockFocusesIt: the other half of the ask — the bar can be
// walked back through without scrolling by eye.
func TestClickingAnOlderBlockFocusesIt(t *testing.T) {
	m := New("dross", "m", "p", nil)
	m.appendTurn(youGutter, "", "first")
	m.appendTurn(youGutter, "", "second")
	// Line indices are into the whole feed, which starts with the masthead.
	off := bannerHeight(m)
	if m.curTurn != m.lineTurn[off+1] {
		t.Fatalf("the newest block should start focused")
	}

	m.focusBlockAt(off)
	if m.curTurn != m.lineTurn[off] {
		t.Fatalf("clicking the first turn's line did not focus its block")
	}
	lines := turnLines(m)
	if !strings.Contains(lines[0], gutterMark) {
		t.Errorf("the clicked block should now wear the bar: %q", lines[0])
	}
	if strings.Contains(lines[1], gutterMark) {
		t.Errorf("the previously focused block should have given it up: %q", lines[1])
	}
	// Out of range is a click on empty space, not a crash.
	m.focusBlockAt(-1)
	m.focusBlockAt(999)
}

// A multi-line block is one block: clicking any of its lines focuses the whole
// thing, and all of its lines wear the bar together.
func TestABlockIsFocusedAsAWhole(t *testing.T) {
	m := New("dross", "m", "p", nil)
	m.appendTurn(youGutter, "", "one\ntwo\nthree")
	m.appendTurn(youGutter, "", "later")

	m.focusBlockAt(bannerHeight(m) + 1) // the middle line of the first block
	lines := turnLines(m)
	for i := 0; i < 3; i++ {
		if !strings.Contains(lines[i], gutterMark) {
			t.Errorf("line %d of the focused block has no bar: %q", i, lines[i])
		}
	}
	if strings.Contains(lines[3], gutterMark) {
		t.Errorf("the unfocused block kept its bar: %q", lines[3])
	}
}

// The parallel slice has to stay the same length as the lines, or a later click
// maps to the wrong block — the failure mode of tracking state beside a slice
// that is spliced in four places.
func TestLineTurnStaysInStepWithTheLines(t *testing.T) {
	m := New("dross", "m", "p", nil)
	for i := 0; i < 5; i++ {
		m.appendTurn(youGutter, "", "block")
	}
	if len(m.lines) != len(m.lineTurn) {
		t.Fatalf("lines %d, lineTurn %d", len(m.lines), len(m.lineTurn))
	}
}

// Streaming rewrites its own block on every token. It truncated m.lines and not
// m.lineTurn, so the index grew past the text it described and every line after
// the drift was attributed to the wrong block — a click then moved the bar
// somewhere the eye had not pointed.
func TestStreamingRewritesKeepTheIndexInStep(t *testing.T) {
	m := New("dross", "m", "p", nil)
	m.appendTurn(youGutter, "", "a question")
	m.newTurn()
	m.streamStart = len(m.lines)
	for _, chunk := range []string{"one", "one two", "one two three", "one two three four"} {
		m.streamBuf = chunk
		m.renderStream()
		if len(m.lines) != len(m.lineTurn) {
			t.Fatalf("after %q: lines %d, lineTurn %d", chunk, len(m.lines), len(m.lineTurn))
		}
	}
}

// Every viewport write has to go through the same composer. Two of them over
// one slice is how the bar came back on resize: layout() re-joined m.lines raw,
// so every block wore one again until the next append.
func TestReflowKeepsTheFocus(t *testing.T) {
	m := New("dross", "m", "p", nil)
	m.width, m.height = 80, 24
	m.appendTurn(youGutter, "", "older")
	m.appendTurn(youGutter, "", "newer")
	m.focusBlockAt(0)
	m.layout()
	if got, want := stripANSI(m.vp.View()), stripANSI(m.body()); !strings.Contains(got, strings.Split(want, "\n")[0]) {
		t.Fatalf("the viewport does not show the focused body after a reflow:\n%q", got)
	}
	if strings.Count(stripANSI(m.body()), gutterMark) != 1 {
		t.Errorf("more than one block wears the bar after a reflow:\n%q", stripANSI(m.body()))
	}
}

// Switching sessions has to carry the index with the text. Left behind, the
// previous session's block map is applied to this one's lines.
func TestSwitchingSessionsCarriesTheIndex(t *testing.T) {
	m := New("dross", "m", "p", nil)
	m.sessions = []session{{}, {}}
	m.appendTurn(youGutter, "", "one\ntwo\nthree")
	m.saveCurrent()
	m.loadSession(1)
	if len(m.lines) != 0 || len(m.lineTurn) != 0 {
		t.Fatalf("a fresh session inherited %d lines / %d index entries", len(m.lines), len(m.lineTurn))
	}
	m.appendTurn(youGutter, "", "elsewhere")
	m.saveCurrent()
	m.loadSession(0)
	if len(m.lines) != len(m.lineTurn) {
		t.Fatalf("lines %d, lineTurn %d after switching back", len(m.lines), len(m.lineTurn))
	}
	if strings.Count(stripANSI(m.body()), gutterMark) != 3 {
		t.Errorf("the restored session lost its focus:\n%q", stripANSI(m.body()))
	}
}

// Crush's split, which this follows: the block in focus wears the thick bar,
// your own blurred turns keep a thin rail, and the agent's go blank. The
// transcript is mostly the agent talking, so blanking those is what clears it.
func TestYourOwnTurnsKeepAThinRail(t *testing.T) {
	m := New("dross", "m", "p", nil)
	m.appendTurn(youGutter, "", "a question")
	m.newTurn() // the agent replies: newTurn + append, not appendTurn
	m.append(agentGutter.Render(gutterMark) + " an answer")
	m.appendTurn(youGutter, "", "a follow-up")

	lines := turnLines(m)
	for i := range lines {
		lines[i] = stripANSI(lines[i])
	}
	if !strings.HasPrefix(lines[0], gutterRail) {
		t.Errorf("your blurred turn should keep a rail: %q", lines[0])
	}
	if strings.HasPrefix(lines[1], gutterRail) || strings.HasPrefix(lines[1], gutterMark) {
		t.Errorf("the agent's blurred turn should go blank: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], gutterMark) {
		t.Errorf("the focused turn should wear the bar: %q", lines[2])
	}
	// Rail, blank and bar are all one column, so the text starts in the same
	// place in all three states and nothing ripples when focus moves.
	for i, ln := range lines {
		if got := runeIndex(ln, 'a'); got != 2 {
			t.Errorf("line %d starts its text at column %d, want 2: %q", i, got, ln)
		}
	}
}

// runeIndex is a column, not a byte offset: the gutter glyphs are multi-byte.
func runeIndex(s string, want rune) int {
	for i, r := range []rune(s) {
		if r == want {
			return i
		}
	}
	return -1
}

// The masthead sits at the top of every transcript (see ensureBanner), so the
// turns these tests are about start below it. Measured rather than hardcoded:
// the logo's height is a rendering detail and not what any of them assert.
func bannerHeight(m Model) int {
	if !m.bannerDone {
		return 0
	}
	return len(strings.Split(m.banner(), "\n"))
}

// turnLines is the body with the masthead dropped, keeping styling intact.
func turnLines(m Model) []string {
	lines := strings.Split(m.body(), "\n")
	if n := bannerHeight(m); n <= len(lines) {
		return lines[n:]
	}
	return lines
}
