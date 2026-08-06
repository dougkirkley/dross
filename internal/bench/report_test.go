package bench

import (
	"context"
	"strings"
	"testing"
	"time"
)

// flakyRunner succeeds only on the runs whose index is in pass.
type flakyRunner struct {
	name string
	pass map[int]bool
	n    int
}

func (f *flakyRunner) Name() string    { return f.name }
func (f *flakyRunner) Available() bool { return true }
func (f *flakyRunner) Exec(_ context.Context, _, wd string) (Metrics, error) {
	i := f.n
	f.n++
	if f.pass[i] {
		if err := seed(wd, map[string]string{"out.txt": "goodbye\n"}); err != nil {
			return Metrics{}, err
		}
	}
	return Metrics{Turns: 3 + i, HasCost: true, Cost: 0.01,
		ModelTime: time.Duration(i+1) * time.Second, ToolTime: 100 * time.Millisecond,
		HasSplit: true}, nil
}

// TestReportShowsSuccessRate is the quality signal a single run hides: an agent
// that solves a task two times in three is not the same as one that solves it
// every time, and on a small model that difference is the whole question.
func TestReportShowsSuccessRate(t *testing.T) {
	task := Task{
		Name:  "greet",
		Check: []string{"sh", "-c", "grep -qx goodbye out.txt"},
	}
	r := &flakyRunner{name: "flaky", pass: map[int]bool{0: true, 2: true}}

	results := Run(context.Background(), []Task{task}, []Runner{r}, 3)
	if len(results) != 3 {
		t.Fatalf("got %d runs, want 3", len(results))
	}
	report := Report(results)
	if !strings.Contains(report, "2/3") {
		t.Fatalf("report should show the success rate:\n%s", report)
	}
	// One row per (task, runner), not one per run.
	if n := strings.Count(report, "greet"); n != 1 {
		t.Fatalf("repeats should collapse into one row, got %d:\n%s", n, report)
	}
}

// TestReportShowsSpread: a median with no range invites a conclusion the
// samples do not support.
//
// Built from fixed durations rather than from a real run — a fake runner does
// no work, so its measured time rounds to zero and there is no spread to find.
func TestReportShowsSpread(t *testing.T) {
	var results []Result
	for _, d := range []time.Duration{2 * time.Second, 5 * time.Second, 9 * time.Second} {
		results = append(results, Result{Runner: "r", Task: "t", Success: true, Duration: d})
	}
	report := Report(results)
	for _, want := range []string{"wall ", "2s", "9s", "–"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report should carry the wall-clock range, missing %q:\n%s", want, report)
		}
	}
	// The median, not the mean: 5s, not 5.33s.
	if !strings.Contains(report, "5s") {
		t.Fatalf("report should show the median:\n%s", report)
	}
}

// TestReportSeparatesToolTime keeps the AGENT column honest: time spent running
// tools is the tools' time, not the loop's, and conflating them hides whichever
// one is actually growing.
func TestReportSeparatesToolTime(t *testing.T) {
	results := []Result{{
		Runner: "dross", Task: "t", Success: true, Duration: 10 * time.Second,
		Metrics: Metrics{ModelTime: 7 * time.Second, ToolTime: 2 * time.Second, HasSplit: true},
	}}
	report := Report(results)
	if !strings.Contains(report, "TOOL") {
		t.Fatalf("report has no TOOL column:\n%s", report)
	}
	// 10s wall - 7s model - 2s tools = 1s of loop.
	if !strings.Contains(report, "1s") {
		t.Fatalf("agent column should be wall minus model minus tools:\n%s", report)
	}
}

// TestMedianIgnoresAnOutlier: one run that hit the timeout must not drag the
// reported number somewhere no run actually went.
func TestMedianIgnoresAnOutlier(t *testing.T) {
	got := median([]time.Duration{2 * time.Second, 3 * time.Second, 10 * time.Minute})
	if got != 3*time.Second {
		t.Fatalf("median = %v, want 3s", got)
	}
}
