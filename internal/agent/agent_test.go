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
	lastInput  string // text of the most recent Send (tool results serialized)
}

func (s *scriptedSession) Send(_ context.Context, in provider.Input) (*provider.Response, error) {
	if s.i == 0 {
		s.firstInput = in.Text
	}
	s.lastInput = in.Text
	for _, tr := range in.ToolResults {
		s.lastInput += tr.Content
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

// TestDelegateParallel proves the lead can delegate several subtasks at once:
// two workers run and both conclusions come back, with usage merged per model.
func TestDelegateParallel(t *testing.T) {
	dir := t.TempDir()
	lead := &scriptedProvider{steps: []provider.Response{
		{
			StopReason: provider.StopToolUse,
			ToolCalls: []provider.ToolCall{
				toolCall("1", "delegate", map[string]any{"tasks": []string{"task A", "task B"}}),
			},
		},
		{StopReason: provider.StopEndTurn, Text: "combined"},
	}}
	work := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopEndTurn, Text: "did it", Usage: provider.Usage{OutputTokens: 5}},
	}}
	ag, err := New(Config{Name: "t", Model: "lead", Workdir: dir}, lead,
		WithWorker(work, "w"), WithMaxParallel(2))
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "do A and B in parallel")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "combined" {
		t.Fatalf("lead text = %q", res.Text)
	}
	u, ok := res.ByModel["w"]
	if !ok || u.OutputTokens < 10 {
		t.Fatalf("worker usage not merged for both subtasks: %+v", res.ByModel)
	}
}

// TestReviewSkill proves the code-review skill: after the lead writes a file, a
// separate reviewer model reviews the diff. Its findings land in Result.Review,
// a "review" event is emitted, and its usage is tracked under the reviewer model.
func TestReviewSkill(t *testing.T) {
	dir := t.TempDir()
	lead := &scriptedProvider{steps: []provider.Response{
		{
			StopReason: provider.StopToolUse,
			ToolCalls: []provider.ToolCall{
				toolCall("1", "write", map[string]string{"path": "a.go", "content": "package a\n"}),
			},
		},
		{StopReason: provider.StopEndTurn, Text: "done"},
	}}
	rev := &scriptedProvider{steps: []provider.Response{
		{
			StopReason: provider.StopEndTurn,
			Text:       "a.go: missing license header.",
			Usage:      provider.Usage{InputTokens: 80, OutputTokens: 12},
		},
	}}

	var kinds []string
	ag, err := New(Config{Name: "t", Model: "lead-model", Workdir: dir}, lead,
		WithReviewer(rev, "reviewer-model"),
		WithEventHook(func(e Event) { kinds = append(kinds, e.Kind) }))
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "write a.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Review, "missing license") {
		t.Fatalf("Review = %q", res.Review)
	}
	if _, ok := res.ByModel["reviewer-model"]; !ok {
		t.Fatalf("reviewer usage not tracked: %v", res.ByModel)
	}
	found := false
	for _, k := range kinds {
		if k == "review" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no review event emitted; got %v", kinds)
	}
}

// TestReviewSkillNoEdits: with a reviewer configured but no file changes, the
// reviewer is never invoked and Review stays empty.
func TestReviewSkillNoEdits(t *testing.T) {
	dir := t.TempDir()
	lead := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopEndTurn, Text: "nothing to change"},
	}}
	rev := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopEndTurn, Text: "should not run"},
	}}
	ag, _ := New(Config{Name: "t", Model: "m", Workdir: dir}, lead,
		WithReviewer(rev, "reviewer-model"))
	res, err := ag.Run(context.Background(), "just answer")
	if err != nil {
		t.Fatal(err)
	}
	if res.Review != "" {
		t.Fatalf("expected no review, got %q", res.Review)
	}
	if rev.last != nil {
		t.Fatal("reviewer session should not have been opened")
	}
}

// TestCompactor proves the token-efficiency skill: a large tool result is
// summarized by the compactor model before it enters the lead's history, its
// usage is tracked, and a "compact" event fires.
func TestCompactor(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("this is a long line of output\n", 400) // > threshold
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	lead := &scriptedProvider{steps: []provider.Response{
		{
			StopReason: provider.StopToolUse,
			ToolCalls: []provider.ToolCall{
				toolCall("1", "read", map[string]string{"path": "big.txt"}),
			},
		},
		{StopReason: provider.StopEndTurn, Text: "done"},
	}}
	comp := &scriptedProvider{steps: []provider.Response{
		{
			StopReason: provider.StopEndTurn,
			Text:       "SUMMARY: 400 identical lines.",
			Usage:      provider.Usage{InputTokens: 200, OutputTokens: 8},
		},
	}}

	var kinds []string
	ag, err := New(Config{Name: "t", Model: "lead", Workdir: dir}, lead,
		WithCompactor(comp, "compactor-model", 0),
		WithEventHook(func(e Event) { kinds = append(kinds, e.Kind) }))
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "read big.txt")
	if err != nil {
		t.Fatal(err)
	}
	// The compacted result the lead saw on turn 2 must be far smaller than raw.
	sess := lead.last
	if !strings.Contains(sess.lastInput, "SUMMARY") {
		t.Fatalf("lead did not receive compacted result; got %q", sess.lastInput)
	}
	if len(sess.lastInput) >= len(big) {
		t.Fatalf("result not compacted: %d >= %d", len(sess.lastInput), len(big))
	}
	if _, ok := res.ByModel["compactor-model"]; !ok {
		t.Fatalf("compactor usage not tracked: %v", res.ByModel)
	}
	found := false
	for _, k := range kinds {
		if k == "compact" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no compact event; got %v", kinds)
	}
}

// TestCompactorSkipsSmall: results under the threshold are passed through
// untouched and the compactor is never invoked.
func TestCompactorSkipsSmall(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "small.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lead := &scriptedProvider{steps: []provider.Response{
		{
			StopReason: provider.StopToolUse,
			ToolCalls: []provider.ToolCall{
				toolCall("1", "read", map[string]string{"path": "small.txt"}),
			},
		},
		{StopReason: provider.StopEndTurn, Text: "done"},
	}}
	comp := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopEndTurn, Text: "should not run"},
	}}
	ag, _ := New(Config{Name: "t", Model: "lead", Workdir: dir}, lead,
		WithCompactor(comp, "compactor-model", 0))
	if _, err := ag.Run(context.Background(), "read small.txt"); err != nil {
		t.Fatal(err)
	}
	if comp.last != nil {
		t.Fatal("compactor session should not have been opened for a small result")
	}
}

// TestFriendlyErr proves the tool-support error is rewritten into an actionable
// message naming the model, and other errors pass through unchanged.
func TestFriendlyErr(t *testing.T) {
	a, err := New(Config{Name: "t", Model: "gemma2:9b", Workdir: t.TempDir()}, &scriptedProvider{})
	if err != nil {
		t.Fatal(err)
	}
	got := a.friendlyErr(errorString("400: registry.ollama.ai/library/gemma2:9b does not support tools"))
	if !strings.Contains(got.Error(), "tool-calling") || !strings.Contains(got.Error(), "gemma2:9b") {
		t.Fatalf("unfriendly: %v", got)
	}
	passthrough := errorString("some other failure")
	if a.friendlyErr(passthrough).Error() != "some other failure" {
		t.Fatalf("passthrough changed: %v", a.friendlyErr(passthrough))
	}
	if a.friendlyErr(nil) != nil {
		t.Fatal("nil should stay nil")
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

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

// TestPublish proves the publish tool reads a workspace file and returns the
// URL the publish callback produces.
func TestPublish(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), []byte("PNGDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{toolCall("p1", "publish", map[string]string{"path": "shot.png"})}},
		{StopReason: provider.StopEndTurn, Text: "shared it"},
	}}
	var gotName, gotMime string
	var gotData []byte
	ag, err := New(Config{Name: "a", Model: "claude-opus-5", Workdir: dir}, prov,
		WithPublish(func(name string, data []byte, mime string) (string, error) {
			gotName, gotData, gotMime = name, data, mime
			return "https://h.example/a/abc", nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), "share the screenshot"); err != nil {
		t.Fatal(err)
	}
	if gotName != "shot.png" || string(gotData) != "PNGDATA" || gotMime != "image/png" {
		t.Fatalf("publish got name=%q mime=%q data=%q", gotName, gotMime, gotData)
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

// TestPermission_PlanBlocksWrites verifies plan mode blocks a write: the file is
// never created and the model gets a "blocked" tool result.
func TestPermission_PlanBlocksWrites(t *testing.T) {
	dir := t.TempDir()
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("1", "write", map[string]string{"path": "x.go", "content": "package main\n"}),
		}},
		{StopReason: provider.StopEndTurn, Text: "planned"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov, WithPermission("plan"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), "write x.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.go")); !os.IsNotExist(err) {
		t.Fatalf("plan mode must not write the file (err=%v)", err)
	}
	if !strings.Contains(prov.last.lastInput, "blocked") {
		t.Fatalf("model should get a blocked result, got %q", prov.last.lastInput)
	}
}

// TestPermission_ManualDeny verifies a denied approval blocks the tool.
func TestPermission_ManualDeny(t *testing.T) {
	dir := t.TempDir()
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("1", "write", map[string]string{"path": "y.go", "content": "package main\n"}),
		}},
		{StopReason: provider.StopEndTurn, Text: "ok"},
	}}
	deny := func(AskRequest) (string, error) { return "Deny", nil }
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov, WithPermission("manual"), WithAsker(deny))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), "write y.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "y.go")); !os.IsNotExist(err) {
		t.Fatal("manual deny must not write the file")
	}
}
