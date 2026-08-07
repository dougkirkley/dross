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
	// Not on the first two — two files one at a time is not yet a habit.
	for i := 0; i < 2; i++ {
		if strings.Contains(results[i], "one at a time") {
			t.Errorf("result %d should not carry the hint yet:\n%s", i+1, results[i])
		}
	}
	if !strings.Contains(results[2], "read(paths=") {
		t.Errorf("the third single read should carry the hint:\n%s", results[2])
	}
	// And not again after that: repeating it every turn is nagging.
	if strings.Contains(results[3], "one at a time") {
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

// TestTakingTheHintResetsIt: a model that switches to the list form stops being
// told, and a later single read starts the count again rather than firing at
// once.
func TestTakingTheHintResetsIt(t *testing.T) {
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
	for i, r := range results {
		if strings.Contains(r, "one at a time") {
			t.Errorf("result %d should not carry the hint — the batch reset the count:\n%s", i+1, r)
		}
	}
}

// TestOtherToolsBreakTheRun: reads spread across a session, with work between
// them, are not the pattern this is for.
func TestOtherToolsBreakTheRun(t *testing.T) {
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
	for i, r := range results {
		if strings.Contains(r, "one at a time") {
			t.Errorf("result %d should not be nudged — the glob broke the run:\n%s", i+1, r)
		}
	}
}
