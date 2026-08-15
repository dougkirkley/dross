package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/pkg/agent"
)

// TestAnOptionMayCarryItsReason: comparing the picker with Claude Code's on the
// same prompt, both drew a tidy list — and Claude's said what each choice cost.
// "SQLite (embedded) — real DB, ACID, query support, still no external server"
// against a bare "Correctness". A label is enough when the choice is already
// understood, and the questions worth asking are the other kind.
func TestAnOptionMayCarryItsReason(t *testing.T) {
	m := New("dross", "m", "p", nil)
	m.width, m.height = 100, 40
	m.pendingAsk = &askState{
		questions: []agent.AskQuestion{{
			Header:   "Store",
			Question: "Which backend?",
			Options: []agent.AskOption{
				{Label: "JSON file", Description: "No deps, human-readable, one process."},
				{Label: "SQLite", Description: "Real queries, still no server to run."},
			},
		}},
		sel:     [][]bool{make([]bool, 2)},
		writeIn: []string{""},
		cursor:  []int{0},
		reply:   make(chan string, 1),
	}
	out := stripANSI(m.renderPicker())
	for _, want := range []string{
		"JSON file", "No deps, human-readable, one process.",
		"SQLite", "Real queries, still no server to run.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the picker dropped %q:\n%s", want, out)
		}
	}
}

// TestABareStringIsStillAnOption: the schema used to be a list of strings and
// models still send that. A string carries no ambiguity — it is the label — so
// it must not fail the call.
func TestABareStringIsStillAnOption(t *testing.T) {
	var q agent.AskQuestion
	if err := json.Unmarshal([]byte(`{"question":"Which?","options":["one",{"label":"two","description":"the second"}]}`), &q); err != nil {
		t.Fatalf("a mixed list should parse: %v", err)
	}
	if len(q.Options) != 2 {
		t.Fatalf("got %d options, want 2", len(q.Options))
	}
	if q.Options[0].Label != "one" || q.Options[0].Description != "" {
		t.Errorf("a bare string should become a label: %+v", q.Options[0])
	}
	if q.Options[1].Label != "two" || q.Options[1].Description != "the second" {
		t.Errorf("an object should keep both halves: %+v", q.Options[1])
	}
}

// TestDecliningAnswersEveryQuestion: a write-in answers in your own words; this
// says the options were the wrong options. It has to leave without visiting the
// other tabs — declining the framing and then being made to answer the rest of
// it would be the same mistake twice.
func TestDecliningAnswersEveryQuestion(t *testing.T) {
	m := New("dross", "m", "p", nil)
	m.width, m.height = 100, 40
	reply := make(chan string, 1)
	m.pendingAsk = &askState{
		questions: []agent.AskQuestion{
			{Header: "A", Question: "First?", Options: []agent.AskOption{{Label: "x"}}},
			{Header: "B", Question: "Second?", Options: []agent.AskOption{{Label: "y"}}},
		},
		sel:     [][]bool{make([]bool, 1), make([]bool, 1)},
		writeIn: []string{"", ""},
		cursor:  []int{0, 0},
		reply:   reply,
	}
	m.declineAsk()

	if m.pendingAsk != nil {
		t.Error("declining should close the picker")
	}
	select {
	case got := <-reply:
		if !strings.Contains(got, "talk it through") {
			t.Errorf("the model should be told to explain rather than assume: %q", got)
		}
	default:
		t.Fatal("declining sent no answer; the turn would hang")
	}
}
