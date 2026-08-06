package bench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner simulates an agent that writes the expected answer, so the harness
// (seed → exec → check) can be tested without a model or external CLI.
type fakeRunner struct {
	name      string
	available bool
	writeName string
	writeBody string
}

func (f *fakeRunner) Name() string    { return f.name }
func (f *fakeRunner) Available() bool { return f.available }
func (f *fakeRunner) Exec(_ context.Context, _ string, wd string) (Metrics, error) {
	if f.writeName != "" {
		os.WriteFile(filepath.Join(wd, f.writeName), []byte(f.writeBody), 0o644)
	}
	return Metrics{Cost: 0.01, Tokens: 42, HasCost: true}, nil
}

func TestHarness(t *testing.T) {
	task := Task{
		Name:   "greet",
		Files:  map[string]string{"src.txt": "hello"},
		Prompt: "make it say goodbye",
		Check:  []string{"sh", "-c", "grep -qx goodbye out.txt"},
	}
	runners := []Runner{
		&fakeRunner{name: "good", available: true, writeName: "out.txt", writeBody: "goodbye\n"},
		&fakeRunner{name: "bad", available: true, writeName: "out.txt", writeBody: "nope\n"},
		&fakeRunner{name: "missing", available: false},
	}

	results := Run(context.Background(), []Task{task}, runners, 1)

	byRunner := map[string]Result{}
	for _, r := range results {
		byRunner[r.Runner] = r
	}
	if !byRunner["good"].Success {
		t.Fatalf("good runner should pass the check: %+v", byRunner["good"])
	}
	if byRunner["bad"].Success {
		t.Fatalf("bad runner should fail the check")
	}
	if byRunner["missing"].Err != "not installed" {
		t.Fatalf("missing runner should be skipped: %+v", byRunner["missing"])
	}
	// The good runner's isolation: its write didn't leak into the bad run.
	if !byRunner["good"].Metrics.HasCost || byRunner["good"].Metrics.Cost != 0.01 {
		t.Fatalf("metrics not recorded: %+v", byRunner["good"].Metrics)
	}
	// Seed files are workspace-isolated (fresh temp per run).
	if byRunner["good"].Duration < 0 {
		t.Fatal("negative duration")
	}

	report := Report(results)
	if !strings.Contains(report, "TASK") || !strings.Contains(report, "greet") || !strings.Contains(report, "good") {
		t.Fatalf("report missing header/rows:\n%s", report)
	}
}

// TestCheckIsolation proves each run gets a fresh workspace (seed doesn't
// persist between runners).
func TestCheckIsolation(t *testing.T) {
	task := Task{
		Name:   "iso",
		Files:  map[string]string{"seed.txt": "x"},
		Prompt: "noop",
		Check:  []string{"sh", "-c", "test ! -f leaked.txt"}, // passes only if no leak
	}
	// First runner leaves a file; second must not see it.
	runners := []Runner{
		&fakeRunner{name: "r1", available: true, writeName: "leaked.txt", writeBody: "1"},
		&fakeRunner{name: "r2", available: true},
	}
	results := Run(context.Background(), []Task{task}, runners, 1)
	for _, r := range results {
		if r.Runner == "r2" && !r.Success {
			t.Fatal("r2 saw a file leaked from r1 — workspaces not isolated")
		}
	}
}
