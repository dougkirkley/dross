package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

const limiterSrc = `package bench

// Clamp returns v limited to the range [lo, hi].
func Clamp(v, lo, hi int) int {
	if v > hi {
		return hi
	}
	if v < lo {
		return hi
	}
	return v
}
`

// TestGrepContextSavesTheFollowUpRead is the round trip this removes: a search
// for a symbol was always followed by a read of the file it was found in.
func TestGrepContextSavesTheFollowUpRead(t *testing.T) {
	w, _ := ws(t, map[string]string{"util.go": limiterSrc})

	out, err := w.grep("if v < lo", grepOpts{Context: 2})
	if err != nil {
		t.Fatal(err)
	}
	// The bug is on the line after the match; without context it is invisible.
	if !strings.Contains(out, "return hi") {
		t.Fatalf("context should show the surrounding lines:\n%s", out)
	}
	if !strings.Contains(out, "util.go") {
		t.Fatalf("output should name the file:\n%s", out)
	}
	// Bounded, not the whole file.
	if strings.Contains(out, "package bench") {
		t.Fatalf("context should not run to the top of the file:\n%s", out)
	}
}

// TestGrepContextIsCapped keeps a search from returning a file a line at a time.
func TestGrepContextIsCapped(t *testing.T) {
	w, _ := ws(t, map[string]string{"util.go": limiterSrc})
	huge, err := w.grep("return v", grepOpts{Context: 10000})
	if err != nil {
		t.Fatal(err)
	}
	capped, err := w.grep("return v", grepOpts{Context: maxGrepContext})
	if err != nil {
		t.Fatal(err)
	}
	if huge != capped {
		t.Fatalf("context should cap at %d", maxGrepContext)
	}
}

// TestGrepFilesOnly answers "where does this live" without the lines.
func TestGrepFilesOnly(t *testing.T) {
	w, _ := ws(t, map[string]string{
		"util.go":      limiterSrc,
		"other.go":     "package bench\n\nfunc use() int { return Clamp(1, 2, 3) }\n",
		"unrelated.go": "package bench\n",
	})
	out, err := w.grep("Clamp", grepOpts{FilesOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"util.go", "other.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("files_only missing %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "unrelated.go") {
		t.Errorf("files_only listed a file with no match:\n%s", out)
	}
	if strings.Contains(out, "func Clamp") {
		t.Errorf("files_only should not return matching lines:\n%s", out)
	}
}

// TestGrepGlobRestrictsFiles: searching a Go symbol should not trawl the docs.
func TestGrepGlobRestrictsFiles(t *testing.T) {
	w, _ := ws(t, map[string]string{
		"util.go":  limiterSrc,
		"notes.md": "Clamp is described here\n",
	})
	out, err := w.grep("Clamp", grepOpts{Glob: "**/*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "notes.md") {
		t.Errorf("glob did not restrict the search:\n%s", out)
	}
	if !strings.Contains(out, "util.go") {
		t.Errorf("glob excluded the file it should have kept:\n%s", out)
	}
}

// TestNoProgressGuardEscalates is the difference between a task that fails and
// a task that times out: a model re-issuing an identical failing call is told
// to stop rather than allowed to spend its whole budget on it.
func TestNoProgressGuardEscalates(t *testing.T) {
	// The same doomed edit, over and over.
	bad := toolCall("1", "edit", map[string]any{
		"path": "a.txt", "old_str": "not in the file", "new_str": "x",
	})
	steps := make([]provider.Response, 0, repeatLimit+1)
	for i := 0; i < repeatLimit; i++ {
		steps = append(steps, provider.Response{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{bad}})
	}
	steps = append(steps, provider.Response{StopReason: provider.StopEndTurn, Text: "gave up"})

	prov := &scriptedProvider{steps: steps}
	w, dir := ws(t, map[string]string{"a.txt": "hello\n"})
	_ = w
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), "edit it"); err != nil {
		t.Fatal(err)
	}
	last := prov.last.lastInput
	if !strings.Contains(last, "Retrying it will not work") {
		t.Fatalf("the third identical failure should be escalated:\n%s", last)
	}
}

// TestNoProgressGuardLeavesOneOffsAlone: a call that fails once and is then
// corrected must not be nagged about.
func TestNoProgressGuardLeavesOneOffsAlone(t *testing.T) {
	_, dir := ws(t, map[string]string{"a.txt": "hello\n"})
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("1", "edit", map[string]any{"path": "a.txt", "old_str": "nope", "new_str": "x"}),
		}},
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("2", "edit", map[string]any{"path": "a.txt", "old_str": "hello", "new_str": "there"}),
		}},
		{StopReason: provider.StopEndTurn, Text: "done"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), "edit it"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prov.last.lastInput, "Retrying it will not work") {
		t.Fatalf("a single failure should not be escalated:\n%s", prov.last.lastInput)
	}
}
