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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Task is one benchmark: seed files into a fresh workspace, run the prompt, then
// run Check (exit 0 = success) to grade the result objectively.
type Task struct {
	Name   string
	Files  map[string]string // path → content, written before the run
	Prompt string
	// Verify is written after the agent has stopped and before Check runs: an
	// acceptance test the agent never sees, and so cannot read the answer out
	// of, weaken, or delete.
	//
	// It is what lets a task be asked the way a person would ask it. A prompt
	// that has to name the test it will be graded by is not a request, it is a
	// specification, and an agent that is handed the specification is being
	// measured on something easier than the job.
	Verify map[string]string
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
	Runner    string
	Task      string
	Success   bool
	Duration  time.Duration
	Metrics   Metrics
	Err       string
	Workspace string // where the run happened, when KeepWorkspaces is set
}

// TaskTimeout bounds a single (runner, task) run, unless a caller overrides it.
// An agent that has gone in circles for ten minutes has already lost the
// comparison; without a bound, one stuck run holds the whole table hostage.
//
// Ten minutes stopped being a safe default the moment a thinking model was in
// the table. ask-for-a-feature has finished at 8m51s and been killed at 10m00s
// on the same model — so the ceiling was deciding the result, and a row that
// says FAIL because the stopwatch ran out is indistinguishable in the table
// from a row that says FAIL because the agent could not do it. Raise it with
// --timeout when the model is slow rather than reading the difference as
// capability.
const TaskTimeout = 10 * time.Minute

// Timeout is the effective per-run bound. Zero means TaskTimeout.
var Timeout time.Duration

func taskTimeout() time.Duration {
	if Timeout > 0 {
		return Timeout
	}
	return TaskTimeout
}

// DefaultRepeat is how many times each (runner, task) pair runs.
//
// One sample is not a measurement. Repeated runs of identical work here vary by
// roughly a third — a model samples differently, a shared GPU is busy, a cache
// is warm — so a single pass cannot tell a real improvement from a lucky roll.
// Three is the smallest number with a meaningful median.
const DefaultRepeat = 3

// Only returns the named tasks, in the order DefaultTasks gives them, and
// reports any name that matched nothing.
//
// Iterating on one task is the common case — a task that fails, or a task being
// written — and running the other six to get at it costs a quarter of an hour
// on a local model.
func Only(tasks []Task, names []string) ([]Task, error) {
	if len(names) == 0 {
		return tasks, nil
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[strings.TrimSpace(n)] = true
	}
	var out []Task
	for _, t := range tasks {
		if want[t.Name] {
			out = append(out, t)
			delete(want, t.Name)
		}
	}
	if len(want) > 0 {
		unknown := make([]string, 0, len(want))
		for n := range want {
			unknown = append(unknown, n)
		}
		sort.Strings(unknown)
		all := make([]string, len(tasks))
		for i, t := range tasks {
			all[i] = t.Name
		}
		return nil, fmt.Errorf("no such task: %s (have: %s)",
			strings.Join(unknown, ", "), strings.Join(all, ", "))
	}
	return out, nil
}

// Hooks report progress while a run is in flight. Both are optional.
//
// A full pass is tens of minutes on a local model, and the table cannot be
// printed until the last run finishes — so without these the terminal sits
// blank from the first task to the last, and there is no way to tell working
// from wedged. Start fires before a run begins, because the useful moment to
// learn that find-in-a-tree takes nine minutes is while it is taking them.
type Hooks struct {
	Start func(runner, task string, i, of int)
	Done  func(Result)
}

// Run executes every available runner over every task and returns every
// individual run. Each (runner, task, repeat) gets its own fresh, isolated
// workspace. repeat < 1 means DefaultRepeat.
func Run(ctx context.Context, tasks []Task, runners []Runner, repeat int) []Result {
	return RunWith(ctx, tasks, runners, repeat, Hooks{})
}

// RunWith is Run with progress reporting.
func RunWith(ctx context.Context, tasks []Task, runners []Runner, repeat int, h Hooks) []Result {
	if repeat < 1 {
		repeat = DefaultRepeat
	}
	var out []Result
	for _, r := range runners {
		if !r.Available() {
			missing := Result{Runner: r.Name(), Task: "(all)", Err: "not installed"}
			if h.Done != nil {
				h.Done(missing)
			}
			out = append(out, missing)
			continue
		}
		for _, task := range tasks {
			for i := 0; i < repeat; i++ {
				if h.Start != nil {
					h.Start(r.Name(), task.Name, i+1, repeat)
				}
				res := runOne(ctx, r, task)
				if h.Done != nil {
					h.Done(res)
				}
				out = append(out, res)
			}
		}
	}
	return out
}

// StreamHooks writes one line per run to w as it happens: the task and runner
// when it starts, the verdict and timing when it ends.
//
// Deliberately not the report's format. This is a progress log — read once,
// while waiting — and aligning it with the summary table would invite reading
// the two as one thing, when only the table has the medians that make a number
// mean anything.
func StreamHooks(w io.Writer) Hooks {
	return Hooks{
		Start: func(runner, task string, i, of int) {
			rep := ""
			if of > 1 {
				rep = fmt.Sprintf(" (%d/%d)", i, of)
			}
			fmt.Fprintf(w, "  → %-18s %s%s\n", task, runner, rep)
		},
		Done: func(r Result) {
			verdict := "ok  "
			if !r.Success {
				verdict = "FAIL"
			}
			turns := ""
			if r.Metrics.Turns > 0 {
				turns = fmt.Sprintf("  %d turns", r.Metrics.Turns)
			}
			note := ""
			if r.Err != "" {
				note = "  " + firstLine(r.Err)
			}
			fmt.Fprintf(w, "    %s %s%s%s\n", verdict, dur(r.Duration), turns, note)
		},
	}
}

// KeepWorkspaces leaves each run's workspace on disk and reports where.
//
// A failing row says a check exited non-zero and nothing else, which is enough
// to know something is wrong and never enough to know what. The workspace is
// the evidence — what the agent actually wrote, and what the hidden acceptance
// test made of it — and it is deleted a microsecond after the verdict.
var KeepWorkspaces bool

func runOne(ctx context.Context, r Runner, task Task) Result {
	res := Result{Runner: r.Name(), Task: task.Name}
	ctx, cancel := context.WithTimeout(ctx, taskTimeout())
	defer cancel()

	wd, err := os.MkdirTemp("", "holobench-")
	if err != nil {
		res.Err = err.Error()
		return res
	}
	if KeepWorkspaces {
		res.Workspace = wd
	} else {
		defer os.RemoveAll(wd)
	}
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
	if err := seed(wd, task.Verify); err != nil {
		res.Err = "verify: " + err.Error()
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

// Parity renders one line per task comparing a dross runner against a baseline
// — normally the Claude Code CLI — on the identical prompt.
//
// The main table has both runners in it already, and reading parity off it
// means holding two rows apart in your head across seven tasks. What matters is
// a single question per task, asked the same way every time: given the same
// words, did they end up in the same place. A task both fail is not a parity
// problem; a task only one passes is the whole point, and which one it is says
// which direction the gap runs.
//
// Turns sit beside the verdict because a task both pass is not necessarily a
// task both did well: the same outcome in twice as many turns is the finding
// that started this benchmark.
func Parity(results []Result, subject, baseline string) string {
	type pair struct{ subj, base []Result }
	byTask := map[string]*pair{}
	var order []string
	for _, r := range results {
		if r.Runner != subject && r.Runner != baseline {
			continue
		}
		p, seen := byTask[r.Task]
		if !seen {
			p = &pair{}
			byTask[r.Task] = p
			order = append(order, r.Task)
		}
		if r.Runner == subject {
			p.subj = append(p.subj, r)
		} else {
			p.base = append(p.base, r)
		}
	}
	if len(order) == 0 {
		return ""
	}
	sort.Strings(order)

	var b strings.Builder
	fmt.Fprintf(&b, "\nparity: %s vs %s, same prompt\n\n", subject, baseline)
	const row = "%-18s %-9s %-9s %-8s %-8s %s\n"
	fmt.Fprintf(&b, row, "TASK", subject, baseline, "TURNS", "TURNS", "VERDICT")
	agreed, compared := 0, 0
	for _, task := range order {
		p := byTask[task]
		// Runs that never reached a model are not evidence about anything, and
		// counting them would turn a spent session limit into "both failed" —
		// which is what happened, and what this drops.
		subj, subjDropped := usableRuns(p.subj)
		base, baseDropped := usableRuns(p.base)
		if len(subj) == 0 || len(base) == 0 {
			why := "not compared"
			if subjDropped > 0 || baseDropped > 0 {
				why = "not compared — no run reached a model"
			}
			fmt.Fprintf(&b, row, task, passRate(p.subj), passRate(p.base), "-", "-", why)
			continue
		}
		compared++
		sOK, bOK := mostlyPassed(subj), mostlyPassed(base)
		verdict := "both failed"
		switch {
		case sOK && bOK:
			verdict, agreed = "both passed", agreed+1
		case sOK:
			verdict = "only " + subject
		case bOK:
			verdict = "only " + baseline + "  <- gap"
		default:
			agreed++ // agreeing that it is too hard is still agreement
		}
		if n := subjDropped + baseDropped; n > 0 {
			verdict += fmt.Sprintf(" (%d run(s) never reached a model, ignored)", n)
		}
		fmt.Fprintf(&b, row, task, passRate(subj), passRate(base),
			turnsOf(subj), turnsOf(base), verdict)
	}
	if compared > 0 {
		fmt.Fprintf(&b, "\nsame outcome on %d of %d tasks compared\n", agreed, compared)
	}
	return b.String()
}

// neverRanUnder is how quickly a "failure" becomes suspicious.
//
// Every runner here has to start a process, reach a model and do some work, so
// nothing genuine finishes in under a few seconds. A row that failed that fast,
// with no turns to show for it, did not attempt the task.
const neverRanUnder = 5 * time.Second

// neverRan reports a run that never reached a model.
//
// The case that matters is the Claude Code CLI meeting its subscription session
// limit: it exits 1 in about a second and a half, and the JSON gives no reason
// at that layer. In a table that is indistinguishable from an agent that tried
// and failed — five of those rows appeared in a real run and were reported as
// 0/5, which reads as "cannot do this task" and means "was never asked".
//
// Three signals together, because any one alone has honest explanations: it
// errored, it reported no turns, and it was too quick to have done anything.
func neverRan(r Result) bool {
	return r.Err != "" && r.Metrics.Turns == 0 && r.Duration < neverRanUnder
}

// usableRuns drops the runs that never happened, and says how many went.
func usableRuns(runs []Result) (kept []Result, dropped int) {
	for _, r := range runs {
		if neverRan(r) {
			dropped++
			continue
		}
		kept = append(kept, r)
	}
	return kept, dropped
}

// mostlyPassed reports whether a runner passed a task more often than not.
//
// The median of the repeats rather than any-pass or all-pass. One lucky run out
// of five is not a capability, and one unlucky run out of five is not a failure
// — this benchmark's own variance is wide enough that both happen.
func mostlyPassed(runs []Result) bool {
	passed := 0
	for _, r := range runs {
		if r.Success {
			passed++
		}
	}
	return passed*2 > len(runs)
}

func passRate(runs []Result) string {
	if len(runs) == 0 {
		return "-"
	}
	passed := 0
	for _, r := range runs {
		if r.Success {
			passed++
		}
	}
	return fmt.Sprintf("%d/%d", passed, len(runs))
}

// turnsOf is the median turn count, or "-" when the runner did not report one.
func turnsOf(runs []Result) string {
	var turns []time.Duration
	for _, r := range runs {
		if r.Metrics.Turns > 0 {
			turns = append(turns, time.Duration(r.Metrics.Turns))
		}
	}
	if len(turns) == 0 {
		return "-"
	}
	return fmt.Sprint(int(median(turns)))
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

	// A kept workspace is named alongside whatever else the row has to say,
	// never instead of it. Replacing the note lost the error that explained the
	// failure and left a path to an empty directory in its place — the one
	// column that said why, overwritten by the flag added to find out why.
	//
	// The failing run's workspace wins: a passing one has nothing to look at.
	workspace := ""
	for _, r := range runs {
		if r.Workspace == "" {
			continue
		}
		if workspace == "" || !r.Success {
			workspace = r.Workspace
		}
		if !r.Success {
			break
		}
	}
	if workspace != "" {
		if notes == "" {
			notes = workspace
		} else {
			notes += "; " + workspace
		}
	}

	// A row where nothing reached a model is not a score. Said plainly, because
	// "0/5" next to a 1.5s wall is the shape a spent Claude session limit takes,
	// and it reads exactly like an agent that cannot do the task.
	if _, dropped := usableRuns(runs); dropped > 0 {
		what := fmt.Sprintf("%d/%d runs never reached a model (too fast to have tried — "+
			"check for a session limit or a bad flag)", dropped, len(runs))
		if notes == "" {
			notes = what
		} else {
			notes = what + "; " + notes
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
