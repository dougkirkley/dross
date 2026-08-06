// Package bench is a comparative benchmark harness: it runs the same coding
// tasks through dross and through external agents (Claude Code, Codex) in
// fresh isolated workspaces, and measures wall-clock time and task success —
// with dross additionally reporting its token cost (the differentiator).
//
// Cross-tool metrics are the honest ones: did the task's check pass, and how
// long did it take. Cost/token accounting is only available for dross (the
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
//
// ModelTime is the part of the wall-clock spent waiting on a model. It is the
// number that makes a cross-tool comparison mean anything: wall-clock alone
// compares whichever models happen to be configured, while wall minus model
// time is the agent's own overhead — the part the harness can actually fix.
type Metrics struct {
	Cost      float64 // USD; 0/unknown when the runner won't say
	Tokens    int64   // total tokens; 0/unknown when the runner won't say
	Turns     int     // model round-trips
	ModelTime time.Duration
	ToolTime  time.Duration // time running tools, reported separately so the
	// remainder is the loop alone rather than the loop plus everything it ran
	HasCost  bool // whether Cost/Tokens are meaningful
	HasSplit bool // whether ModelTime/ToolTime are meaningful
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

// TaskTimeout bounds a single (runner, task) run. An agent that has gone in
// circles for ten minutes has already lost the comparison; without this one
// stuck run holds the whole table hostage.
const TaskTimeout = 10 * time.Minute

// DefaultRepeat is how many times each (runner, task) pair runs.
//
// One sample is not a measurement. Repeated runs of identical work here vary by
// roughly a third — a model samples differently, a shared GPU is busy, a cache
// is warm — so a single pass cannot tell a real improvement from a lucky roll.
// Three is the smallest number with a meaningful median.
const DefaultRepeat = 3

// Run executes every available runner over every task and returns every
// individual run. Each (runner, task, repeat) gets its own fresh, isolated
// workspace. repeat < 1 means DefaultRepeat.
func Run(ctx context.Context, tasks []Task, runners []Runner, repeat int) []Result {
	if repeat < 1 {
		repeat = DefaultRepeat
	}
	var out []Result
	for _, r := range runners {
		if !r.Available() {
			out = append(out, Result{Runner: r.Name(), Task: "(all)", Err: "not installed"})
			continue
		}
		for _, task := range tasks {
			for i := 0; i < repeat; i++ {
				out = append(out, runOne(ctx, r, task))
			}
		}
	}
	return out
}

func runOne(ctx context.Context, r Runner, task Task) Result {
	res := Result{Runner: r.Name(), Task: task.Name}
	ctx, cancel := context.WithTimeout(ctx, TaskTimeout)
	defer cancel()

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

// dur formats a duration for the table, or "-" when there is nothing to say.
func dur(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return d.Round(time.Millisecond).String()
}

// median returns the middle value, or the lower of the two middles for an even
// count. The median rather than the mean because one run that hit the timeout
// would drag an average somewhere no run actually went.
func median(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[(len(s)-1)/2]
}

// Report renders results as a table, one row per (task, runner), aggregating
// that pair's repeats.
//
// Every timing is a median with its range beside it, because a single number
// invites a conclusion the samples do not support: a change that moves the
// median by less than the spread has not been shown to do anything.
func Report(results []Result) string {
	type key struct{ task, runner string }
	var order []key
	groups := map[key][]Result{}
	for _, r := range results {
		k := key{r.Task, r.Runner}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].task != order[j].task {
			return order[i].task < order[j].task
		}
		return order[i].runner < order[j].runner
	})

	var b []byte
	add := func(s string) { b = append(b, s...) }
	const row = "%-16s %-14s %-6s %-10s %-10s %-9s %-9s %-6s %-10s %s\n"
	add(fmt.Sprintf(row, "TASK", "RUNNER", "OK", "WALL", "MODEL", "TOOL", "AGENT", "TURNS", "COST", "NOTES"))
	for _, k := range order {
		add(reportRow(row, groups[k]))
	}
	return string(b)
}

// reportRow renders one (task, runner) pair's repeats as a single line.
func reportRow(format string, runs []Result) string {
	var walls, models, tools, agents []time.Duration
	var turns []time.Duration // durations only so median() can be reused
	var costs []float64
	passed, hasCost, hasSplit := 0, false, false
	notes := ""
	for _, r := range runs {
		if r.Success {
			passed++
		}
		if r.Err != "" && notes == "" {
			notes = firstLine(r.Err)
		}
		if r.Duration > 0 {
			walls = append(walls, r.Duration)
		}
		if r.Metrics.HasSplit {
			hasSplit = true
			models = append(models, r.Metrics.ModelTime)
			tools = append(tools, r.Metrics.ToolTime)
			// What is left once the model has answered and the tools have run:
			// the loop itself, and whatever the provider costs to reach.
			agents = append(agents, r.Duration-r.Metrics.ModelTime-r.Metrics.ToolTime)
		}
		if r.Metrics.HasCost {
			hasCost = true
			costs = append(costs, r.Metrics.Cost)
		}
		if r.Metrics.Turns > 0 {
			turns = append(turns, time.Duration(r.Metrics.Turns))
		}
	}

	ok := fmt.Sprintf("%d/%d", passed, len(runs))
	// A pair that never produced a timing is a pair that never ran; its note
	// carries the reason.
	wall := dur(median(walls))
	if len(walls) > 1 {
		lo, hi := median(walls), median(walls)
		for _, w := range walls {
			if w < lo {
				lo = w
			}
			if w > hi {
				hi = w
			}
		}
		if spread := fmt.Sprintf("wall %s–%s", dur(lo), dur(hi)); notes == "" {
			notes = spread
		} else {
			notes = spread + "; " + notes
		}
	}
	model, tool, agent := "-", "-", "-"
	if hasSplit {
		model, tool, agent = dur(median(models)), dur(median(tools)), dur(median(agents))
	}
	turnStr := "-"
	if len(turns) > 0 {
		turnStr = fmt.Sprint(int(median(turns)))
	}
	cost := "-"
	if hasCost {
		var total float64
		for _, c := range costs {
			total += c
		}
		cost = fmt.Sprintf("$%.4f", total/float64(len(costs)))
	}
	return fmt.Sprintf(format, runs[0].Task, runs[0].Runner, ok, wall, model, tool, agent, turnStr, cost, notes)
}
