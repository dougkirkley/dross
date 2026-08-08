package agent

import (
	"context"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

// Nothing set a temperature for a long time, so every backend's own default
// applied — around 0.7 on ollama — and the same task took a different path on
// every run. In a tool-calling loop that shows up as a different number of
// turns, not a different turn of phrase.
func TestCodeSessionsSampleLow(t *testing.T) {
	a := &Agent{cfg: Config{}}
	got := a.temperature()
	if got == nil {
		t.Fatal("a coding session should ask for a temperature rather than take the backend's")
	}
	if *got != codeTemperature {
		t.Errorf("temperature = %v, want %v", *got, codeTemperature)
	}
	// Low, not zero: exactly zero makes a small model likelier to repeat a call
	// it has already made, which is the failure repeatLimit exists to catch.
	if *got <= 0 {
		t.Errorf("temperature = %v, want above zero", *got)
	}
}

// Prose is the one thing the variety is good for.
func TestChatSessionsAreLeftAlone(t *testing.T) {
	a := &Agent{cfg: Config{Chat: true}}
	if got := a.temperature(); got != nil {
		t.Errorf("a chat session should keep the backend's default, got %v", *got)
	}
}

func TestAnExplicitTemperatureWins(t *testing.T) {
	want := 0.9
	a := &Agent{cfg: Config{Temperature: &want}}
	got := a.temperature()
	if got == nil || *got != want {
		t.Errorf("temperature = %v, want %v", got, want)
	}
	// Including for a chat session, where the default is to say nothing.
	a = &Agent{cfg: Config{Chat: true, Temperature: &want}}
	if got := a.temperature(); got == nil || *got != want {
		t.Errorf("an explicit choice should hold for chat too: %v", got)
	}
}

// Zero is a real request, not an absent one — which is why the field is a
// pointer. Sending nothing and sending 0 ask for different things.
func TestZeroIsARealTemperature(t *testing.T) {
	zero := 0.0
	a := &Agent{cfg: Config{Temperature: &zero}}
	got := a.temperature()
	if got == nil {
		t.Fatal("zero must not read as unset")
	}
	if *got != 0 {
		t.Errorf("temperature = %v, want 0", *got)
	}
}

// The session actually carries it, or none of the above reaches the wire.
func TestTheSessionIsToldTheTemperature(t *testing.T) {
	p := &recordingProvider{}
	seed := 7
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir(), Seed: &seed}, p)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ag.Run(t.Context(), "hello")
	if ag == nil {
		t.Fatal("no agent")
	}
	if p.cfg.Temperature == nil || *p.cfg.Temperature != codeTemperature {
		t.Errorf("session temperature = %v", p.cfg.Temperature)
	}
	if p.cfg.Seed == nil || *p.cfg.Seed != seed {
		t.Errorf("session seed = %v, want %d", p.cfg.Seed, seed)
	}
}

// recordingProvider captures the SessionConfig it was handed.
type recordingProvider struct{ cfg provider.SessionConfig }

func (r *recordingProvider) Name() string { return "recording" }
func (r *recordingProvider) NewSession(cfg provider.SessionConfig) provider.Session {
	r.cfg = cfg
	return &recordingSession{}
}

type recordingSession struct{}

func (recordingSession) Send(ctx context.Context, in provider.Input) (*provider.Response, error) {
	return &provider.Response{Text: "done", StopReason: provider.StopEndTurn}, nil
}
