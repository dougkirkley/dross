package tui

import (
	"strings"
	"testing"

	"github.com/dougkirkley/dross/pkg/agent"
)

// TestTheLogoStaysAtTheTopOfTheTranscript: the splash used to be an alternative
// body, shown while there was nothing to show and gone the instant the first
// line arrived — so the one moment the session said what it was ran before
// anything happened, and a screenshot of real work carried no identity at all.
//
// It is written into the feed now, which is also what makes it scroll away: it
// is the top of the conversation rather than a header pinned over it.
func TestTheLogoStaysAtTheTopOfTheTranscript(t *testing.T) {
	m := New("dross", "qwen3-coder:30b", "openai", nil)
	m.width, m.height = 100, 40
	m.appendTurn(youGutter, "", "a question")

	body := stripANSI(m.body())
	if !strings.Contains(body, "qwen3-coder:30b") {
		t.Errorf("the masthead should name the model it is running:\n%s", body)
	}
	if !strings.Contains(body, "a question") {
		t.Errorf("the turn went missing under the masthead:\n%s", body)
	}
	// Above the conversation, not below it.
	if strings.Index(body, "qwen3-coder:30b") > strings.Index(body, "a question") {
		t.Error("the masthead should come before the first turn")
	}
}

// TestTheMastheadIsWrittenOnce: it is part of the feed, so a second turn must
// not bring a second copy.
func TestTheMastheadIsWrittenOnce(t *testing.T) {
	m := New("dross", "widget-model", "p", nil)
	m.width, m.height = 100, 40
	for i := 0; i < 3; i++ {
		m.appendTurn(youGutter, "", "turn")
	}
	if n := strings.Count(stripANSI(m.body()), "widget-model"); n != 1 {
		t.Errorf("the masthead appears %d times, want 1", n)
	}
	if len(m.lines) != len(m.lineTurn) {
		t.Fatalf("lines %d, lineTurn %d — the masthead broke the block index",
			len(m.lines), len(m.lineTurn))
	}
}

// TestAPreambleDoesNotBreakTheToolGroup is the collapsing. The group machinery
// was already there; a one-line "let me check the other files" between calls
// closed it, so three greps became three "searched 1 pattern" lines with a
// sentence wedged between each instead of one "searched 3 patterns".
func TestAPreambleDoesNotBreakTheToolGroup(t *testing.T) {
	m := New("dross", "m", "p", nil)
	m.width, m.height = 100, 40
	grep := func() { m.applyEvent(agent.Event{Kind: "tool", Data: `grep {"pattern":"retry"}`}) }

	grep()
	m.applyEvent(agent.Event{Kind: "text", Data: "Let me check the other files for retry logic."})
	grep()
	m.applyEvent(agent.Event{Kind: "text", Data: "Checking whether anything else configures this."})
	grep()

	body := stripANSI(m.body())
	if !strings.Contains(body, "Searched 3 patterns") {
		t.Errorf("the three searches should read as one group:\n%s", body)
	}
	if strings.Contains(body, "Searched 1 pattern") {
		t.Errorf("a preamble still split the group:\n%s", body)
	}
}

// TestARealReplyStillEndsTheGroup: the fold is for the model talking to itself.
// An actual answer is a turn, and has to look like one.
func TestARealReplyStillEndsTheGroup(t *testing.T) {
	m := New("dross", "m", "p", nil)
	m.width, m.height = 100, 40
	m.applyEvent(agent.Event{Kind: "tool", Data: `grep {"pattern":"retry"}`})

	long := "The failure is in the retry budget. " + strings.Repeat(
		"MaxRetries is three where the slow backend needs five, so the run gives up early. ", 3)
	m.applyEvent(agent.Event{Kind: "text", Data: long})
	m.applyEvent(agent.Event{Kind: "tool", Data: `grep {"pattern":"backoff"}`})

	body := stripANSI(m.body())
	// Two groups, because a real reply came between them.
	if n := strings.Count(body, "Searched 1 pattern"); n != 2 {
		t.Errorf("a real reply should end the group, giving two of them, got %d:\n%s", n, body)
	}
}
