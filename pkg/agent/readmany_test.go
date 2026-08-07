package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

// TestReadManyInOneCall is the round trip this removes. A turn's read-only
// calls already run at the same time, but that only helps a model that puts
// several of them in one message — and gpt-oss:20b was measured at one call per
// turn, and kept to it even when told in as many words to send three together.
// So the batching moves into the call.
func TestReadManyInOneCall(t *testing.T) {
	w, _ := ws(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\n",
		"c.go": "package c\n",
	})

	out, err := w.readFiles([]string{"a.go", "b.go", "c.go"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"=== a.go ===", "package a", "=== b.go ===", "package b", "=== c.go ===", "package c"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// Still line-numbered, so the model can talk about ranges as before.
	if !strings.Contains(out, "     1\t") {
		t.Errorf("content should keep its line numbers:\n%s", out)
	}
}

// TestOneFileReadsExactlyAsBefore: a single path must not gain a header, or
// every existing caller's output changes for no reason.
func TestOneFileReadsExactlyAsBefore(t *testing.T) {
	w, _ := ws(t, map[string]string{"a.go": "package a\n"})
	many, err := w.readFiles([]string{"a.go"})
	if err != nil {
		t.Fatal(err)
	}
	one, err := w.readFile("a.go")
	if err != nil {
		t.Fatal(err)
	}
	if many != one {
		t.Fatalf("one path should read identically:\n%q\nvs\n%q", many, one)
	}
}

// TestUnreadableFileDoesNotSinkTheCall: the other three were fine, and
// re-asking for them is the round trip this is trying to avoid.
func TestUnreadableFileDoesNotSinkTheCall(t *testing.T) {
	w, _ := ws(t, map[string]string{"a.go": "package a\n"})

	out, err := w.readFiles([]string{"a.go", "gone.go"})
	if err != nil {
		t.Fatalf("one bad path should not fail the call: %v", err)
	}
	if !strings.Contains(out, "package a") {
		t.Errorf("the readable file should still come back:\n%s", out)
	}
	if !strings.Contains(out, "could not read") {
		t.Errorf("the missing file should be reported in place:\n%s", out)
	}
}

// TestAllUnreadableIsAnError: nothing came back, so this is a failed call
// rather than a result with notes.
func TestAllUnreadableIsAnError(t *testing.T) {
	w, _ := ws(t, map[string]string{})
	if _, err := w.readFiles([]string{"x.go", "y.go"}); err == nil {
		t.Fatal("want an error when nothing could be read")
	}
	if _, err := w.readFiles(nil); err == nil {
		t.Fatal("want an error for no paths at all")
	}
}

// TestReadIsBounded stops a model pulling a repository into the context window
// with one call.
func TestReadIsBounded(t *testing.T) {
	w, _ := ws(t, map[string]string{"a.go": "package a\n"})
	many := make([]string, maxReadPaths+1)
	for i := range many {
		many[i] = "a.go"
	}
	_, err := w.readFiles(many)
	if err == nil {
		t.Fatal("want an error past the limit")
	}
	if !strings.Contains(err.Error(), fmt.Sprint(maxReadPaths)) {
		t.Errorf("the error should name the limit: %v", err)
	}
}

// TestReadAcceptsBothForms: a model that hedges by sending path and paths
// together should get everything it asked for, not one of the two silently.
func TestReadAcceptsBothForms(t *testing.T) {
	_, dir := ws(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n"})
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("1", "read", map[string]any{"path": "a.go", "paths": []string{"b.go"}}),
		}},
		{StopReason: provider.StopEndTurn, Text: "read them"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), "read both"); err != nil {
		t.Fatal(err)
	}
	last := prov.last.lastInput
	if !strings.Contains(last, "package a") || !strings.Contains(last, "package b") {
		t.Fatalf("both forms should be honoured:\n%s", last)
	}
}

// TestPromptOffersTheBatchedRead: the tool can do it, so the model has to be
// told — this whole change exists because a model would not batch on its own.
func TestPromptOffersTheBatchedRead(t *testing.T) {
	p := basePrompt([]string{"read", "grep", "bash"}, false)
	if !strings.Contains(p, "paths") {
		t.Fatalf("prompt never mentions reading several files at once:\n%s", p)
	}
}
