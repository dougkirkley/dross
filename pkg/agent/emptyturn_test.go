package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

// TestEmptyTurnIsNotAFinishedTask is the failure this guards: a local backend
// answered 200 with no text, no tool call and no tokens, and the run ended in
// one turn reporting success with the bug still in the file.
func TestEmptyTurnIsNotAFinishedTask(t *testing.T) {
	_, dir := ws(t, map[string]string{"a.txt": "hello\n"})
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopEndTurn}, // the empty completion
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("1", "edit", map[string]any{"path": "a.txt", "old_str": "hello", "new_str": "there"}),
		}},
		{StopReason: provider.StopEndTurn, Text: "fixed it"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "fix a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if res.Turns < 3 {
		t.Fatalf("turns = %d — an empty reply ended the run instead of being re-asked", res.Turns)
	}
	if res.Text != "fixed it" {
		t.Fatalf("text = %q", res.Text)
	}
}

// TestEmptyTurnNudgeSaysWhatToDo: the re-ask has to leave a genuinely finished
// model a way to stop, or it becomes a loop.
func TestEmptyTurnNudgeSaysWhatToDo(t *testing.T) {
	_, dir := ws(t, map[string]string{})
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopEndTurn},
		{StopReason: provider.StopEndTurn, Text: "nothing to do, the file already says goodbye"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "check it")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prov.last.seen(), "empty") {
		t.Errorf("the model was not told its reply was empty: %q", prov.last.seen())
	}
	if res.Text == "" {
		t.Error("a model that answers in words after the nudge should be believed")
	}
}

// TestEmptyTurnIsNudgedOnce keeps the guard from becoming a loop of its own: a
// backend that only ever returns nothing must still terminate.
func TestEmptyTurnIsNudgedOnce(t *testing.T) {
	_, dir := ws(t, map[string]string{})
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopEndTurn},
		{StopReason: provider.StopEndTurn},
		{StopReason: provider.StopEndTurn},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "do something")
	if err != nil {
		t.Fatal(err)
	}
	if res.Turns > 2 {
		t.Fatalf("turns = %d — a silent backend should be re-asked once, not forever", res.Turns)
	}
}

// erraticProvider fails the first n sends with err, then replays steps.
type erraticProvider struct {
	fails int
	err   error
	steps []provider.Response
}

func (p *erraticProvider) Name() string { return "erratic" }
func (p *erraticProvider) NewSession(provider.SessionConfig) provider.Session {
	return &erraticSession{p: p}
}

type erraticSession struct {
	p    *erraticProvider
	i    int
	seen []string
}

func (s *erraticSession) Send(_ context.Context, in provider.Input) (*provider.Response, error) {
	s.seen = append(s.seen, in.Text)
	if s.p.fails > 0 {
		s.p.fails--
		return nil, s.p.err
	}
	r := s.p.steps[s.i]
	s.i++
	return &r, nil
}

// TestMalformedToolCallIsRepairedNotFatal is the run that died four minutes in:
// ollama rejected the model's own tool call, and the whole task ended on it.
func TestMalformedToolCallIsRepairedNotFatal(t *testing.T) {
	_, dir := ws(t, map[string]string{"a.txt": "hello\n"})
	prov := &erraticProvider{
		fails: 1,
		err:   fmt.Errorf("openai: 500: %w", provider.ErrMalformedToolCall),
		steps: []provider.Response{
			{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
				toolCall("1", "edit", map[string]any{"path": "a.txt", "old_str": "hello", "new_str": "there"}),
			}},
			{StopReason: provider.StopEndTurn, Text: "fixed"},
		},
	}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "fix a.txt")
	if err != nil {
		t.Fatalf("a rejected tool call should be repairable, not fatal: %v", err)
	}
	if res.Text != "fixed" {
		t.Fatalf("text = %q", res.Text)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(got) != "there\n" {
		t.Fatalf("the edit never landed: %q", got)
	}
}

// TestMalformedToolCallGivesUpEventually: a model that cannot produce a
// well-formed call is not going to on the tenth try, and each costs a round
// trip.
func TestMalformedToolCallGivesUpEventually(t *testing.T) {
	_, dir := ws(t, map[string]string{})
	prov := &erraticProvider{
		fails: 100,
		err:   fmt.Errorf("openai: 500: %w", provider.ErrMalformedToolCall),
	}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "do it")
	if err == nil {
		t.Fatal("want the error once the repair attempts are spent")
	}
	if res.Turns > malformedLimit+1 {
		t.Fatalf("turns = %d, want it bounded near %d", res.Turns, malformedLimit)
	}
}

// TestWordsWithoutToolsSurviveTheChallenge: a model that explains itself is
// asked once whether it meant to do the work, and standing by the answer ends
// the run.
//
// This test used to assert one turn, on the reasoning that a model which
// explains itself has finished. Reported use says otherwise, and often: the
// reply is not a conclusion but an intention — "I'll change the comparison in
// util.go" — and taking it at face value ends the session with the file
// untouched and an answer that reads as though the work was done. See
// noWorkNudge and TestNarratedWorkIsPushedBackOn.
//
// So a toolless answer now costs one extra turn. That is the price of the
// guard, paid here on the case where the answer was genuine — and it is the
// smaller of the two errors, because the other one wastes the whole run. Note
// that even this reply is a claim about a workspace nothing in the run ever
// opened.
func TestWordsWithoutToolsSurviveTheChallenge(t *testing.T) {
	_, dir := ws(t, map[string]string{})
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopEndTurn, Text: "The test already passes; nothing to change."},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "check it")
	if err != nil {
		t.Fatal(err)
	}
	if res.Turns != 2 {
		t.Fatalf("turns = %d, want 2 — the answer, and one challenge it stands by", res.Turns)
	}
}
