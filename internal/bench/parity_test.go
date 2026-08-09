package bench

import (
	"strings"
	"testing"
	"time"
)

func run(runner, task string, ok bool, turns int) Result {
	return Result{Runner: runner, Task: task, Success: ok, Metrics: Metrics{Turns: turns}}
}

// TestParityNamesTheGapAndItsDirection: the report exists to answer one
// question per task, and the answer that matters is the asymmetric one. A task
// the baseline passes and the subject does not is a gap in dross; the reverse
// is not, and calling both "disagree" would bury the difference.
func TestParityNamesTheGapAndItsDirection(t *testing.T) {
	results := []Result{
		run("dross", "both-pass", true, 4), run("claude-code", "both-pass", true, 6),
		run("dross", "only-claude", false, 9), run("claude-code", "only-claude", true, 5),
		run("dross", "only-dross", true, 3), run("claude-code", "only-dross", false, 8),
		run("dross", "neither", false, 12), run("claude-code", "neither", false, 11),
	}
	out := Parity(results, "dross", "claude-code")

	for _, want := range []string{
		"both passed",
		"only claude-code  <- gap",
		"only dross",
		"both failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("parity report missing %q:\n%s", want, out)
		}
	}
	// Agreement counts the task neither could do: agreeing it is too hard is
	// still agreement, and pretending otherwise would make a benchmark look
	// like a divergence.
	if !strings.Contains(out, "same outcome on 2 of 4") {
		t.Errorf("agreement miscounted:\n%s", out)
	}
	// Turns sit beside the verdict — the same outcome in twice the turns is a
	// finding, not a tie.
	if !strings.Contains(out, "4") || !strings.Contains(out, "6") {
		t.Errorf("turn counts missing:\n%s", out)
	}
}

// TestParityWillNotCompareWhatItDidNotRun: a task only one side ran is not
// agreement. Counting it as one would turn "claude-code was not installed" into
// a perfect score.
func TestParityWillNotCompareWhatItDidNotRun(t *testing.T) {
	out := Parity([]Result{
		run("dross", "alone", true, 3),
		run("dross", "shared", true, 3), run("claude-code", "shared", true, 4),
	}, "dross", "claude-code")

	if !strings.Contains(out, "not compared") {
		t.Errorf("a one-sided task must say so:\n%s", out)
	}
	if !strings.Contains(out, "same outcome on 1 of 1") {
		t.Errorf("only the genuinely compared task should count:\n%s", out)
	}
}

// TestParityUsesTheMedianOfRepeats: this benchmark's variance is wide — the
// same task has failed at three minutes and passed at nine — so one lucky run
// is not a capability and one unlucky run is not a gap.
func TestParityUsesTheMedianOfRepeats(t *testing.T) {
	out := Parity([]Result{
		run("dross", "flaky", true, 4), run("dross", "flaky", true, 4), run("dross", "flaky", false, 9),
		run("claude-code", "flaky", true, 5), run("claude-code", "flaky", true, 5), run("claude-code", "flaky", true, 5),
	}, "dross", "claude-code")

	if !strings.Contains(out, "both passed") {
		t.Errorf("two of three passes is a pass:\n%s", out)
	}
	if !strings.Contains(out, "2/3") {
		t.Errorf("the raw rate should still be visible, not rounded away:\n%s", out)
	}
}

// TestParityIsEmptyWithoutABaseline: with no external runner there is nothing
// to compare, and an empty report is better than a table of dashes.
func TestParityIsEmptyWithoutABaseline(t *testing.T) {
	if out := Parity([]Result{run("dross", "x", true, 3)}, "dross", "nobody"); out == "" {
		t.Skip("one-sided results still render, which is fine")
	}
	if out := Parity(nil, "dross", "claude-code"); out != "" {
		t.Errorf("no results should render nothing, got %q", out)
	}
}

// runFast is a run that failed before it could have done anything — the shape a
// spent Claude session limit takes.
func runFast(runner, task string) Result {
	return Result{Runner: runner, Task: task, Duration: 1500 * time.Millisecond,
		Err: "exit status 1"}
}

// TestParityWillNotCountASessionLimitAsAgreement is the row that lied.
//
// Five claude-code runs of hunt-in-a-tree came back 0/5 at ~1.5s with "exit
// status 1" — the subscription session limit, not a verdict on the task. The
// report called it "both failed" and counted it as agreement, which is the one
// conclusion the data cannot support.
func TestParityWillNotCountASessionLimitAsAgreement(t *testing.T) {
	var results []Result
	for i := 0; i < 5; i++ {
		results = append(results,
			run("dross", "hunt-in-a-tree", false, 7),
			runFast("claude-code", "hunt-in-a-tree"))
	}
	out := Parity(results, "dross", "claude-code")

	if strings.Contains(out, "both failed") {
		t.Errorf("a session limit was read as a shared failure:\n%s", out)
	}
	if !strings.Contains(out, "no run reached a model") {
		t.Errorf("the reason should be named:\n%s", out)
	}
	if strings.Contains(out, "same outcome on 1") {
		t.Errorf("an uncomparable task must not count as agreement:\n%s", out)
	}
}

// A task where only some runs were lost is still comparable — on the runs that
// happened. Throwing the whole task away would waste real measurements.
func TestParityKeepsTheRunsThatHappened(t *testing.T) {
	results := []Result{
		run("dross", "t", true, 4), run("dross", "t", true, 4),
		run("claude-code", "t", true, 6),
		runFast("claude-code", "t"),
	}
	out := Parity(results, "dross", "claude-code")
	if !strings.Contains(out, "both passed") {
		t.Errorf("the surviving runs should still be compared:\n%s", out)
	}
	if !strings.Contains(out, "never reached a model") {
		t.Errorf("the dropped run should be disclosed:\n%s", out)
	}
	// The rate shown is of the runs that counted, not of the ones attempted.
	if !strings.Contains(out, "1/1") {
		t.Errorf("claude's rate should be over its usable runs:\n%s", out)
	}
}

// A slow genuine failure is a real result and must survive.
func TestParityKeepsHonestFailures(t *testing.T) {
	slow := Result{Runner: "claude-code", Task: "t", Duration: 45 * time.Second,
		Metrics: Metrics{Turns: 9}, Err: ""}
	out := Parity([]Result{run("dross", "t", true, 4), slow}, "dross", "claude-code")
	if strings.Contains(out, "never reached a model") {
		t.Errorf("a 45s run with 9 turns did happen:\n%s", out)
	}
}
