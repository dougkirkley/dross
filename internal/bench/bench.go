// Package bench is a comparative benchmark harness: it runs the same coding
// tasks through ergon and through external agents (Claude Code, Codex) in
// fresh isolated workspaces, and measures wall-clock time and task success —
// with ergon additionally reporting its token cost (the differentiator).
//
// Cross-tool metrics are the honest ones: did the task's check pass, and how
// long did it take. Cost/token accounting is only available for ergon (the
// external CLIs don't expose it), so it shows "-" for them.
package bench

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

// Task is one benchmark: seed files into a fresh workspace, run the prompt, then
// run Check (exit 0 = success) to grade the result objectively.
type Task struct {
	Name   string
	Files  map[string]string // path → content, written before the run
	Prompt string
	Check  []string // shell argv run in the workspace; exit 0 = pass
}

// Metrics are what a runner reports about executing one prompt.
type Metrics struct {
	Cost    float64 // USD; 0/unknown for external tools
	Tokens  int64   // total tokens; 0/unknown for external tools
	HasCost bool    // whether Cost/Tokens are meaningful
}

// Runner executes a prompt in a workspace using a particular agent.
type Runner interface {
	Name() string
	Available() bool
	Exec(ctx context.Context, prompt, workdir string) (Metrics, error)
}

// Result is one (runner, task) outcome.
type Result struct {
	Runner   string
	Task     string
	Success  bool
	Duration time.Duration
	Metrics  Metrics
	Err      string
}

// Run executes every available runner over every task and returns the results.
// Each (runner, task) gets its own fresh, isolated workspace.
func Run(ctx context.Context, tasks []Task, runners []Runner) []Result {
	var out []Result
	for _, r := range runners {
		if !r.Available() {
			out = append(out, Result{Runner: r.Name(), Task: "(all)", Err: "not installed"})
			continue
		}
		for _, task := range tasks {
			out = append(out, runOne(ctx, r, task))
		}
	}
	return out
}

func runOne(ctx context.Context, r Runner, task Task) Result {
	res := Result{Runner: r.Name(), Task: task.Name}

	wd, err := os.MkdirTemp("", "holobench-")
	if err != nil {
		res.Err = err.Error()
		return res
	}
	defer os.RemoveAll(wd)
	if err := seed(wd, task.Files); err != nil {
		res.Err = "seed: " + err.Error()
		return res
	}

	start := time.Now()
	m, err := r.Exec(ctx, task.Prompt, wd)
	res.Duration = time.Since(start)
	res.Metrics = m
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.Success = check(wd, task.Check)
	return res
}

func seed(wd string, files map[string]string) error {
	for p, content := range files {
		abs := filepath.Join(wd, p)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// check runs the task's grading command in the workspace.
func check(wd string, argv []string) bool {
	if len(argv) == 0 {
		return true
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = wd
	return cmd.Run() == nil
}

// Report renders results as a table.
func Report(results []Result) string {
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Task != results[j].Task {
			return results[i].Task < results[j].Task
		}
		return results[i].Runner < results[j].Runner
	})
	var b []byte
	add := func(s string) { b = append(b, s...) }
	add(fmt.Sprintf("%-16s %-12s %-8s %-10s %-10s %s\n", "TASK", "RUNNER", "OK", "TIME", "COST", "NOTES"))
	for _, r := range results {
		ok := "no"
		if r.Success {
			ok = "yes"
		}
		cost := "-"
		if r.Metrics.HasCost {
			cost = fmt.Sprintf("$%.4f", r.Metrics.Cost)
		}
		t := "-"
		if r.Duration > 0 {
			t = r.Duration.Round(time.Millisecond).String()
		}
		add(fmt.Sprintf("%-16s %-12s %-8s %-10s %-10s %s\n", r.Task, r.Runner, ok, t, cost, r.Err))
	}
	return string(b)
}
