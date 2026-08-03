package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

// toollessProvider refuses any session created with tools, the way ollama does
// for a model without tool-calling, and answers plainly once asked without.
type toollessProvider struct {
	toolSessions int
	bareSessions int
}

func (p *toollessProvider) Name() string { return "toolless" }
func (p *toollessProvider) NewSession(cfg provider.SessionConfig) provider.Session {
	if len(cfg.Tools) > 0 {
		p.toolSessions++
		return &erroringSession{err: errors.New(`registry.ollama.ai/library/gemma2:9b does not support tools`)}
	}
	p.bareSessions++
	return &scriptedSession{steps: []provider.Response{
		{StopReason: provider.StopEndTurn, Text: "answered from the search results"},
	}}
}

type erroringSession struct{ err error }

func (s *erroringSession) Send(context.Context, provider.Input) (*provider.Response, error) {
	return nil, s.err
}

// Chat is built search-first: the results ride in with the prompt, so a model
// that cannot call tools can still do the job. The turn has to fall back to a
// tool-less session rather than surface an error about agent sessions nobody
// asked for.
func TestChatFallsBackWhenModelHasNoTools(t *testing.T) {
	prov := &toollessProvider{}
	ag, err := New(Config{Name: "t", Model: "gemma2:9b", Workdir: t.TempDir(), Chat: true}, prov)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "best mattresses?")
	if err != nil {
		t.Fatalf("chat should have fallen back, got: %v", err)
	}
	if res.Text != "answered from the search results" {
		t.Errorf("text = %q", res.Text)
	}
	if prov.toolSessions != 1 || prov.bareSessions != 1 {
		t.Errorf("sessions with/without tools = %d/%d, want 1/1",
			prov.toolSessions, prov.bareSessions)
	}
}

// Code sessions keep the refusal: an agent that cannot call tools cannot edit
// a file, and the honest failure is the error that says so.
func TestCodeStillRefusesToollessModels(t *testing.T) {
	prov := &toollessProvider{}
	ag, err := New(Config{Name: "t", Model: "gemma2:9b", Workdir: t.TempDir()}, prov)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ag.Run(context.Background(), "edit main.go")
	if err == nil || prov.bareSessions != 0 {
		t.Fatalf("code should refuse a tool-less model, got err=%v bare=%d", err, prov.bareSessions)
	}
}
