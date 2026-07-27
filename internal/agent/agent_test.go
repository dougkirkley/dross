package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dougkirkley/holocron/internal/provider"
)

// scriptedProvider replays a fixed list of responses, one per Send, so a run can
// be driven without hitting a real model.
type scriptedProvider struct {
	steps []provider.Response
	last  *scriptedSession // most recently opened session, for inspection
}

func (p *scriptedProvider) Name() string { return "scripted" }
func (p *scriptedProvider) NewSession(provider.SessionConfig) provider.Session {
	s := &scriptedSession{steps: p.steps}
	p.last = s
	return s
}

type scriptedSession struct {
	steps      []provider.Response
	i          int
	firstInput string
}

func (s *scriptedSession) Send(_ context.Context, in provider.Input) (*provider.Response, error) {
	if s.i == 0 {
		s.firstInput = in.Text
	}
	r := s.steps[s.i]
	s.i++
	return &r, nil
}

func toolCall(id, name string, input any) provider.ToolCall {
	b, _ := json.Marshal(input)
	return provider.ToolCall{ID: id, Name: name, Input: b}
}

// TestRun_WritesThenEdits drives the full loop: the agent writes a file, edits
// it, then finishes. Asserts the file on disk reflects both operations —
// proving tool dispatch, the write/edit tools, and loop termination.
func TestRun_WritesThenEdits(t *testing.T) {
	dir := t.TempDir()

	prov := &scriptedProvider{steps: []provider.Response{
		{
			StopReason: provider.StopToolUse,
			ToolCalls: []provider.ToolCall{
				toolCall("1", "write", map[string]string{"path": "hello.go", "content": "package main\n\nvar x = 1\n"}),
			},
		},
		{
			StopReason: provider.StopToolUse,
			ToolCalls: []provider.ToolCall{
				toolCall("2", "edit", map[string]string{"path": "hello.go", "old_str": "var x = 1", "new_str": "var x = 42"}),
			},
		},
		{StopReason: provider.StopEndTurn, Text: "done"},
	}}

	ag, err := New(Config{Name: "t", Model: "claude-opus-5", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "make x = 42")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "done" || res.Turns != 3 {
		t.Fatalf("unexpected result: text=%q turns=%d", res.Text, res.Turns)
	}

	got, err := os.ReadFile(filepath.Join(dir, "hello.go"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "package main\n\nvar x = 42\n"; string(got) != want {
		t.Fatalf("file mismatch:\ngot  %q\nwant %q", got, want)
	}
}

// TestDelegate proves the lead delegates a subtask to the worker model: the
// worker writes the file, the lead finishes, and usage is tracked per model so
// cost reflects the split (worker model here is priced 0, i.e. local).
func TestDelegate(t *testing.T) {
	dir := t.TempDir()

	lead := &scriptedProvider{steps: []provider.Response{
		{
			StopReason: provider.StopToolUse,
			ToolCalls:  []provider.ToolCall{toolCall("d1", "delegate", map[string]string{"task": "create f.txt containing ok"})},
			Usage:      provider.Usage{InputTokens: 100, OutputTokens: 20},
		},
		{StopReason: provider.StopEndTurn, Text: "delegated and done", Usage: provider.Usage{InputTokens: 50, OutputTokens: 10}},
	}}
	work := &scriptedProvider{steps: []provider.Response{
		{
			StopReason: provider.StopToolUse,
			ToolCalls:  []provider.ToolCall{toolCall("w1", "write", map[string]string{"path": "f.txt", "content": "ok"})},
			Usage:      provider.Usage{InputTokens: 2000, OutputTokens: 40},
		},
		{StopReason: provider.StopEndTurn, Text: "wrote f.txt", Usage: provider.Usage{InputTokens: 500, OutputTokens: 15}},
	}}

	ag, err := New(
		Config{Name: "lead", Model: "claude-opus-5", Workdir: dir},
		lead,
		WithWorker(work, "qwen3-coder:30b"),
	)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "make f.txt say ok")
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ok" {
		t.Fatalf("file = %q, want ok", got)
	}

	// Both models tracked; worker's larger input is priced free (local).
	if _, ok := res.ByModel["claude-opus-5"]; !ok {
		t.Fatal("lead usage not tracked")
	}
	if _, ok := res.ByModel["qwen3-coder:30b"]; !ok {
		t.Fatal("worker usage not tracked")
	}
	// Cost = only the paid lead tokens: (150 in * $5 + 30 out * $25)/1e6.
	wantCost := (150*5.0 + 30*25.0) / 1_000_000
	if c := res.Cost(); c < wantCost-1e-9 || c > wantCost+1e-9 {
		t.Fatalf("cost = %.8f, want %.8f (worker should be free)", c, wantCost)
	}
}

// TestCheckpoint proves a run produces a handoff brief and hands it to the save
// callback (an extra model turn after the loop).
func TestCheckpoint(t *testing.T) {
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopEndTurn, Text: "task done"},
		{StopReason: provider.StopEndTurn, Text: "BRIEF: did the thing"}, // brief turn
	}}

	var saved string
	ag, err := New(Config{Name: "t", Model: "claude-opus-5", Workdir: t.TempDir()}, prov,
		WithCheckpoint(func(b string) error { saved = b; return nil }))
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "do a thing")
	if err != nil {
		t.Fatal(err)
	}
	if saved != "BRIEF: did the thing" || res.Brief != saved {
		t.Fatalf("brief not saved: saved=%q res.Brief=%q", saved, res.Brief)
	}
	if res.Turns != 2 {
		t.Fatalf("turns = %d, want 2 (main + brief)", res.Turns)
	}
}

// TestResume proves a saved brief is seeded into the first turn's input.
func TestResume(t *testing.T) {
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopEndTurn, Text: "ok"},
	}}
	ag, err := New(Config{Name: "t", Model: "claude-opus-5", Workdir: t.TempDir()}, prov,
		WithResume("PRIOR STATE: half done"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), "keep going"); err != nil {
		t.Fatal(err)
	}
	first := prov.last.firstInput
	if !strings.Contains(first, "PRIOR STATE: half done") || !strings.Contains(first, "keep going") {
		t.Fatalf("first input missing resume or prompt: %q", first)
	}
}

// TestEdit_Uniqueness proves the edit tool refuses ambiguous and absent matches
// (surfaced to the model as a tool error, not a crash).
func TestEdit_Uniqueness(t *testing.T) {
	ws := &workspace{root: t.TempDir()}
	if _, err := ws.writeFile("f.txt", "a\na\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.editFile("f.txt", "a", "b"); err == nil {
		t.Fatal("expected error for ambiguous match")
	}
	if _, err := ws.editFile("f.txt", "zzz", "b"); err == nil {
		t.Fatal("expected error for absent match")
	}
}

// TestWorkspace_Confinement proves paths escaping the workspace are rejected.
func TestWorkspace_Confinement(t *testing.T) {
	ws := &workspace{root: t.TempDir()}
	for _, bad := range []string{"../escape", "../../etc/passwd", "/etc/passwd"} {
		if _, err := ws.resolve(bad); err == nil {
			t.Fatalf("resolve(%q) should have failed", bad)
		}
	}
	if _, err := ws.resolve("sub/ok.txt"); err != nil {
		t.Fatalf("resolve of in-tree path failed: %v", err)
	}
}
