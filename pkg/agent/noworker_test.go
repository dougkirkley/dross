package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

// countingProvider fails the test if a session is ever opened on it.
type countingProvider struct{ sessions int }

func (p *countingProvider) Name() string { return "counting" }
func (p *countingProvider) NewSession(provider.SessionConfig) provider.Session {
	p.sessions++
	return &scriptedSession{steps: []provider.Response{{StopReason: provider.StopEndTurn}}}
}

// With no worker configured, delegation must cost nothing: the tool is not
// offered, and calling it anyway must not open a session on any backend.
func TestNoWorkerMeansNoDelegation(t *testing.T) {
	prov := &countingProvider{}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, prov)
	if err != nil {
		t.Fatal(err)
	}

	for _, tl := range ag.enabledTools() {
		if tl.Name == "delegate" {
			t.Error("delegate offered with no worker configured")
		}
	}

	// Called regardless — a model can name a tool it was never given — it
	// answers immediately rather than reaching for a model.
	before := prov.sessions
	out, isErr := ag.delegate(context.Background(), "do a thing", &Result{})
	if !isErr || !strings.Contains(out, "not configured") {
		t.Errorf("delegate returned %q, isErr=%v; want a plain refusal", out, isErr)
	}
	if prov.sessions != before {
		t.Errorf("opened %d session(s) with no worker; want none", prov.sessions-before)
	}
}

// The tool appears once a worker exists, so the guard above is about
// configuration and not about the tool being missing altogether.
func TestWorkerEnablesDelegation(t *testing.T) {
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()},
		&countingProvider{}, WithWorker(&countingProvider{}, "worker-model"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range ag.enabledTools() {
		if tl.Name == "delegate" {
			return
		}
	}
	t.Error("delegate not offered despite a worker")
}
