package bench

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestStreamReportsBeforeAndAfterEachRun: the point is knowing something is
// happening while it happens. A line only on completion still leaves a
// nine-minute silence during the run that is actually slow.
func TestStreamReportsBeforeAndAfterEachRun(t *testing.T) {
	var buf bytes.Buffer
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
	RunWith(context.Background(), []Task{task}, runners, 1, StreamHooks(&buf))
	out := buf.String()

	if strings.Count(out, "→") != 2 {
		t.Errorf("want a start line per run of an available runner:\n%s", out)
	}
	if !strings.Contains(out, "ok  ") {
		t.Errorf("the passing run should say so:\n%s", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("the failing run should say so:\n%s", out)
	}
	// A runner that is not installed has no run to start, but its absence is
	// still news — silently skipping it is how a missing claude turns into a
	// table nobody notices is half empty.
	if !strings.Contains(out, "not installed") {
		t.Errorf("an unavailable runner should be reported:\n%s", out)
	}
}

// The repeat counter only appears when there is more than one, so a single pass
// is not decorated with "(1/1)" on every line.
func TestStreamCountsRepeatsOnlyWhenThereAreSome(t *testing.T) {
	task := Task{Name: "t", Prompt: "p", Check: []string{"true"}}
	runner := []Runner{&fakeRunner{name: "r", available: true}}

	var one bytes.Buffer
	RunWith(context.Background(), []Task{task}, runner, 1, StreamHooks(&one))
	if strings.Contains(one.String(), "1/1") {
		t.Errorf("a single run should not be numbered:\n%s", one.String())
	}

	var three bytes.Buffer
	RunWith(context.Background(), []Task{task}, runner, 3, StreamHooks(&three))
	for _, want := range []string{"(1/3)", "(2/3)", "(3/3)"} {
		if !strings.Contains(three.String(), want) {
			t.Errorf("missing %s:\n%s", want, three.String())
		}
	}
}

// Run still works with no hooks at all, and returns what it always did — the
// streaming is an addition, not a new requirement on every caller.
func TestRunWithoutHooksIsUnchanged(t *testing.T) {
	task := Task{Name: "t", Prompt: "p", Check: []string{"true"}}
	got := Run(context.Background(), []Task{task},
		[]Runner{&fakeRunner{name: "r", available: true}}, 2)
	if len(got) != 2 {
		t.Fatalf("want 2 results, got %d", len(got))
	}
	for _, r := range got {
		if !r.Success {
			t.Fatalf("a check of `true` should pass: %+v", r)
		}
	}
}
