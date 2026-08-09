package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/internal/bench"
)

// TestBenchCommandWiring runs the real command against an endpoint that refuses
// connections, so every run fails in milliseconds and the output can be
// inspected without a model.
//
// This test exists because three separate wirings — streaming, --timeout, and
// the parity report — were each written, unit-tested, and reported as done
// while the line that connected them to the command silently failed to apply.
// The library had tests. The command had none, so nothing noticed that the
// flags did nothing. What is checked here is not the behaviour of any of those
// features but that the command reaches them at all.
func TestBenchCommandWiring(t *testing.T) {
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"bench",
		// Port 1 is never listening, so each run fails at once.
		"--dross", "provider=openai,model=m,base_url=http://127.0.0.1:1/v1,api_key=x,label=dross",
		"--external=false",
		"--task", "edit-output",
		"--repeat", "2",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("bench should report failures, not return an error: %v", err)
	}
	got := out.String()

	// Streaming: a line as each run starts, and a verdict as it ends.
	if !strings.Contains(got, "→") {
		t.Errorf("no progress lines — streaming is not wired:\n%s", got)
	}
	if strings.Count(got, "→") != 2 {
		t.Errorf("want one start line per repeat:\n%s", got)
	}
	if !strings.Contains(got, "FAIL") {
		t.Errorf("a run against a dead endpoint should stream a FAIL:\n%s", got)
	}
	// And the summary table still follows the stream.
	if !strings.Contains(got, "TASK") || !strings.Contains(got, "edit-output") {
		t.Errorf("summary table missing:\n%s", got)
	}
}

// TestBenchTimeoutFlagIsApplied: the flag parsed and was then dropped on the
// floor, so a run kept the ten-minute default while the table implied
// otherwise. Checked through the package variable the command sets, because
// observing a real timeout would mean waiting for one.
func TestBenchTimeoutFlagIsApplied(t *testing.T) {
	bench.Timeout = 0
	t.Cleanup(func() { bench.Timeout = 0 })

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"bench",
		"--dross", "provider=openai,model=m,base_url=http://127.0.0.1:1/v1,api_key=x",
		"--external=false", "--task", "edit-output", "--repeat", "1",
		"--timeout", "17m",
	})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if bench.Timeout.String() != "17m0s" {
		t.Fatalf("--timeout reached the harness as %v; the flag is not wired", bench.Timeout)
	}
}

// TestBenchParityPrintsWithAnExternalRunner: the parity section is the whole
// point of --external, and it was never printed.
//
// codex and claude may or may not be installed here, so what is asserted is
// that the section appears at all — an uninstalled baseline still produces the
// header and an honest "not compared".
func TestBenchParityPrintsWithAnExternalRunner(t *testing.T) {
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"bench",
		"--dross", "provider=openai,model=m,base_url=http://127.0.0.1:1/v1,api_key=x,label=dross",
		"--external",
		"--task", "edit-output",
		"--repeat", "1",
		// Nothing may actually run; the section must still be attempted.
		"--timeout", "5s",
	})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "parity:") {
		t.Fatalf("no parity section with --external:\n%s", out.String())
	}
}

// TestBenchSpecThinking: the key exists so one table can hold the same model
// with and without deliberation, which only works if the two get separate rows.
func TestBenchSpecThinking(t *testing.T) {
	unset, err := parseDrossSpec("provider=openai,model=qwen3:14b")
	if err != nil {
		t.Fatal(err)
	}
	if unset.thinking != nil {
		t.Error("unmentioned thinking must stay nil, so the model keeps its own default")
	}

	off, err := parseDrossSpec("provider=openai,model=qwen3:14b,thinking=false")
	if err != nil {
		t.Fatal(err)
	}
	if off.thinking == nil || *off.thinking {
		t.Fatalf("thinking=false parsed as %v", off.thinking)
	}
	on, err := parseDrossSpec("provider=openai,model=qwen3:14b,thinking=true")
	if err != nil {
		t.Fatal(err)
	}
	if on.thinking == nil || !*on.thinking {
		t.Fatalf("thinking=true parsed as %v", on.thinking)
	}
	// Distinct labels, or the two configurations collapse into one row and the
	// comparison the key exists for cannot be read.
	if off.label == on.label || off.label == unset.label {
		t.Fatalf("labels collide: unset=%q off=%q on=%q", unset.label, off.label, on.label)
	}
	if _, err := parseDrossSpec("provider=openai,thinking=maybe"); err == nil {
		t.Error("a value that is neither true nor false should be refused, not guessed at")
	}
}
