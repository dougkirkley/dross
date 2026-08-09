package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

// readCalls builds n single-file read turns followed by a final answer.
func readCalls(files []string) []provider.Response {
	steps := make([]provider.Response, 0, len(files)+1)
	for i, f := range files {
		steps = append(steps, provider.Response{
			StopReason: provider.StopToolUse,
			ToolCalls:  []provider.ToolCall{toolCall(string(rune('a'+i)), "read", map[string]any{"path": f})},
		})
	}
	return append(steps, provider.Response{StopReason: provider.StopEndTurn, Text: "read them"})
}

func fourFiles(t *testing.T) string {
	t.Helper()
	_, dir := ws(t, map[string]string{
		"a.go": "package a\n", "b.go": "package b\n",
		"c.go": "package c\n", "d.go": "package d\n",
	})
	return dir
}

// TestHintArrivesWhenItIsActionable: the tool description says read takes a
// list and the system prompt says it too, and a small model still opened files
// one per round trip about half the time. This says it at the moment the model
// is demonstrably in the habit.
func TestHintArrivesWhenItIsActionable(t *testing.T) {
	dir := fourFiles(t)
	prov := &scriptedProvider{steps: readCalls([]string{"a.go", "b.go", "c.go", "d.go"})}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	var results []string
	ag.SetEventHook(func(e Event) {
		if e.Kind == "tool-result" {
			results = append(results, e.Data)
		}
	})
	if _, err := ag.Run(context.Background(), "read them all"); err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 {
		t.Fatalf("got %d results, want 4", len(results))
	}
	// Not on the first two — two single reads is not yet a habit. Matched on
	// "per call", which only the batching hint says: "one at a time" also
	// appears in the neighbourhood outline appended to a single-file read, and
	// matching that made this test fail on a block it was not about.
	for i := 0; i < 2; i++ {
		if strings.Contains(results[i], "per call") {
			t.Errorf("result %d should not carry the hint yet:\n%s", i+1, results[i])
		}
	}
	if !strings.Contains(results[2], "read(paths=") {
		t.Errorf("the third single read should carry the hint:\n%s", results[2])
	}
	// And not again after that: repeating it every turn is nagging.
	if strings.Contains(results[3], "per call") {
		t.Errorf("the hint should be said once, not every turn:\n%s", results[3])
	}
}

// TestBatchedReadIsNotNagged: a model that already asks for several files at
// once must never see the hint.
func TestBatchedReadIsNotNagged(t *testing.T) {
	dir := fourFiles(t)
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("1", "read", map[string]any{"paths": []string{"a.go", "b.go", "c.go", "d.go"}}),
		}},
		{StopReason: provider.StopEndTurn, Text: "done"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	var results []string
	ag.SetEventHook(func(e Event) {
		if e.Kind == "tool-result" {
			results = append(results, e.Data)
		}
	})
	if _, err := ag.Run(context.Background(), "read them"); err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if strings.Contains(r, "one at a time") {
			t.Errorf("a batched read should never be nudged:\n%s", r)
		}
	}
}

// TestTwoFilesIsNotTakingTheHint overturns what this test used to assert.
//
// It required consecutive single reads and treated any call with two paths as
// batching, which cleared the count. That made the hint unreachable in the case
// it exists for: a traced session read 1, 1, 2, 1, 2, 1, 3, 1 files across
// fifteen turns and was never told once, because every two-file read reset it.
// Two of a possible twenty is not a batch. The rule is now the average size of
// a fetch, so this sequence — five files across four calls — is still a habit
// worth naming.
func TestTwoFilesIsNotTakingTheHint(t *testing.T) {
	dir := fourFiles(t)
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{toolCall("1", "read", map[string]any{"path": "a.go"})}},
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{toolCall("2", "read", map[string]any{"path": "b.go"})}},
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("3", "read", map[string]any{"paths": []string{"c.go", "d.go"}}),
		}},
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{toolCall("4", "read", map[string]any{"path": "a.go"})}},
		{StopReason: provider.StopEndTurn, Text: "done"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	var results []string
	ag.SetEventHook(func(e Event) {
		if e.Kind == "tool-result" {
			results = append(results, e.Data)
		}
	})
	if _, err := ag.Run(context.Background(), "read them"); err != nil {
		t.Fatal(err)
	}
	hinted := false
	for _, r := range results {
		if strings.Contains(r, "per call") {
			hinted = true
		}
	}
	if !hinted {
		t.Errorf("five files across four calls averages 1.25 and should be named:\n%v", results)
	}
}

// TestOtherToolsDoNotBreakTheRun is the other rule this change overturned.
//
// A glob or a todo between reads used to clear the count, on the reasoning that
// reads spread across a session are not the pattern. Real sessions are exactly
// that shape — the traced one interleaved globs and todo writes throughout —
// so resetting on any other tool was a second way for the hint never to fire.
// The count is cumulative for the run.
func TestOtherToolsDoNotBreakTheRun(t *testing.T) {
	dir := fourFiles(t)
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{toolCall("1", "read", map[string]any{"path": "a.go"})}},
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{toolCall("2", "read", map[string]any{"path": "b.go"})}},
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{toolCall("3", "glob", map[string]any{"pattern": "*.go"})}},
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{toolCall("4", "read", map[string]any{"path": "c.go"})}},
		{StopReason: provider.StopEndTurn, Text: "done"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	var results []string
	ag.SetEventHook(func(e Event) {
		if e.Kind == "tool-result" {
			results = append(results, e.Data)
		}
	})
	if _, err := ag.Run(context.Background(), "look around"); err != nil {
		t.Fatal(err)
	}
	hinted := false
	for _, r := range results {
		if strings.Contains(r, "per call") {
			hinted = true
		}
	}
	if !hinted {
		t.Errorf("three single reads with a glob between them is still three single reads:\n%v", results)
	}
}
