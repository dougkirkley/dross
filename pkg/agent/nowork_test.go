package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

// TestNarratedWorkIsPushedBackOn is the reported failure, reproduced.
//
// The model answers a "fix this bug" prompt with a description of the fix and
// no tool call. That is prose plus an end_turn, which the loop used to accept
// as a finished task — the session ended, the file was untouched, and the
// answer read as though the work had been done.
func TestNarratedWorkIsPushedBackOn(t *testing.T) {
	prov := &scriptedProvider{steps: []provider.Response{
		// Turn one: the description of work, which is not work.
		{StopReason: provider.StopEndTurn, Text: "I'll change the comparison in util.go so values below the range clamp to the low bound."},
		// Turn two, after the push-back: it actually does it.
		{
			StopReason: provider.StopToolUse,
			ToolCalls:  []provider.ToolCall{toolCall("1", "write", map[string]string{"path": "util.go", "content": "package bench\n"})},
		},
		{StopReason: provider.StopEndTurn, Text: "Fixed."},
	}}
	dir := t.TempDir()
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "fix the clamp bug")
	if err != nil {
		t.Fatal(err)
	}
	if res.Turns < 3 {
		t.Fatalf("ran %d turns; the narrated turn should have been challenged, not accepted", res.Turns)
	}
	if !strings.Contains(strings.Join(prov.last.inputs, "\n"), "without having used a single tool") {
		t.Fatalf("the push-back never reached the model; inputs were %q", prov.last.inputs)
	}
	// And it produced the work, which is the whole point: the file the narrated
	// turn described is on disk.
	if _, err := os.Stat(filepath.Join(dir, "util.go")); err != nil {
		t.Fatalf("util.go was still never written: %v", err)
	}
}

// TestWorkThatHappenedIsNotPushedBackOn: the challenge is about a run that did
// nothing, not about a run that finished. A model that used a tool and then
// stopped has done the job and must be left alone.
func TestWorkThatHappenedIsNotPushedBackOn(t *testing.T) {
	prov := &scriptedProvider{steps: []provider.Response{
		{
			StopReason: provider.StopToolUse,
			ToolCalls:  []provider.ToolCall{toolCall("1", "write", map[string]string{"path": "f.go", "content": "package x\n"})},
		},
		{StopReason: provider.StopEndTurn, Text: "Done."},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, prov)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "write f.go")
	if err != nil {
		t.Fatal(err)
	}
	if res.Turns != 2 {
		t.Fatalf("ran %d turns; a run that did work should finish when it says it is finished", res.Turns)
	}
}

// TestTheChallengeIsMadeOnlyOnce: a model that answers the push-back with more
// prose is telling you it has nothing to do. Asking again would be an argument,
// and on a local model an expensive one.
func TestTheChallengeIsMadeOnlyOnce(t *testing.T) {
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopEndTurn, Text: "I would change util.go."},
		{StopReason: provider.StopEndTurn, Text: "As I said, I would change util.go."},
		{StopReason: provider.StopEndTurn, Text: "Still the same."},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, prov)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "fix it")
	if err != nil {
		t.Fatal(err)
	}
	if res.Turns != 2 {
		t.Fatalf("ran %d turns; want 2 — the answer, one challenge, and then take it", res.Turns)
	}
}

// TestChatIsNeverPushedBackOn: a chat session has no filesystem, so answering
// without touching one is the only thing it can do. Challenging that would
// make every conversational reply cost a second turn to say the same thing.
func TestChatIsNeverPushedBackOn(t *testing.T) {
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopEndTurn, Text: "A pool reuses open connections."},
		{StopReason: provider.StopEndTurn, Text: "unwanted second turn"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir(), Chat: true}, prov)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "explain connection pooling")
	if err != nil {
		t.Fatal(err)
	}
	if res.Turns != 1 {
		t.Fatalf("ran %d turns; a chat answer is finished when it is given", res.Turns)
	}
}

// TestSearchedButChangedNothingIsPushedBackOn is the reported failure, taken
// from a real trace.
//
// Asked to find the component whose rate disagrees with its docs and fix it,
// qwen2.5-coder:14b ran one grep, answered "the internal/reporting package
// seems to be reporting a rate that doesn't match what its own docs say — let's
// check the documentation and code for discrepancies", and ended the turn. The
// answer was right. The file was never touched. Two turns, and a transcript
// that reads like success.
//
// The zero-tool guard could not see it: the run used a tool. What it never did
// was change anything.
func TestSearchedButChangedNothingIsPushedBackOn(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rate.go"), []byte("package x\n\n// Rate is 100.\nfunc Rate() int { return 107 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("1", "grep", map[string]any{"pattern": "Rate is"}),
		}},
		// Found it, described the next step, stopped.
		{StopReason: provider.StopEndTurn, Text: "rate.go disagrees with its docs. Let's check and correct it."},
		// After the challenge, it does the work.
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("2", "edit", map[string]any{"path": "rate.go", "old_str": "return 107", "new_str": "return 100"}),
		}},
		{StopReason: provider.StopEndTurn, Text: "fixed"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), "find the component that disagrees with its docs and fix it"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prov.last.seen(), "without changing anything") {
		t.Fatalf("the challenge never reached the model; inputs were %q", prov.last.inputs)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "rate.go"))
	if !strings.Contains(string(got), "return 100") {
		t.Fatalf("the fix never landed:\n%s", got)
	}
}

// A run that did change something is finished when it says so — searching and
// then editing must not be challenged.
func TestAnEditEndsTheRunCleanly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("1", "edit", map[string]any{"path": "a.go", "old_str": "package x", "new_str": "package y"}),
		}},
		{StopReason: provider.StopEndTurn, Text: "done"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "rename the package")
	if err != nil {
		t.Fatal(err)
	}
	if res.Turns != 2 {
		t.Fatalf("turns = %d; a run that edited should not be challenged", res.Turns)
	}
}

// TestTheGuardKeepsWorkingLaterInAConversation is the bug this nearly shipped
// with: the agent is reused across prompts and a.edits keeps growing, so a
// naive len(a.edits) == 0 would be true only for the very first prompt and the
// guard would silently stop working exactly where a long session needs it.
func TestTheGuardKeepsWorkingLaterInAConversation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prov := &scriptedProvider{steps: []provider.Response{
		// First prompt: a real edit.
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("1", "edit", map[string]any{"path": "a.go", "old_str": "package x", "new_str": "package y"}),
		}},
		{StopReason: provider.StopEndTurn, Text: "done"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov, WithConversation())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), "rename it"); err != nil {
		t.Fatal(err)
	}
	if len(ag.edits) == 0 {
		t.Fatal("precondition: the first prompt should have recorded an edit")
	}

	// Second prompt on the same agent: narrates, changes nothing. Must still be
	// challenged even though the agent has edits from before.
	prov.steps = []provider.Response{
		{StopReason: provider.StopEndTurn, Text: "I'll update the other file too."},
	}
	res, err := ag.Run(context.Background(), "now do the other one")
	if err != nil {
		t.Fatal(err)
	}
	if res.Turns < 2 {
		t.Fatalf("turns = %d; the second prompt changed nothing and should have been challenged", res.Turns)
	}
}
