package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/dougkirkley/dross/pkg/agent"
	"github.com/dougkirkley/dross/pkg/provider"
)

// DrossRunner runs tasks with the in-process agent, so it reports real cost.
//
// Label distinguishes several dross configurations in one table. Comparing
// dross-on-a-local-model, dross-on-Claude and native Claude Code side by side
// is the comparison that says anything: the first pair isolates the model, the
// second isolates the agent.
type DrossRunner struct {
	Label       string
	Provider    provider.Provider
	Model       string
	Worker      provider.Provider // optional
	WorkerModel string
	// Thinking, when set, turns a reasoning model's deliberation on or off —
	// the difference between a turn that costs seconds and one that costs most
	// of a minute.
	Thinking *bool

	runs int // repeat counter, so each run gets its own reproducible seed
}

func (h *DrossRunner) Name() string {
	if h.Label != "" {
		return h.Label
	}
	return "dross"
}
func (h *DrossRunner) Available() bool { return h.Provider != nil }

// seedFor hands out the run's sampler seed and advances the counter.
//
// Backends that ignore seed (Anthropic, the Claude Code CLI) are unaffected.
//
// What this does NOT buy is a repeatable run, and it is worth being exact about
// that because the claim used to be here. The seed reaches the wire — the
// openai provider sends it, and temperature is pinned at 0.1 — and two passes
// over read-a-big-log still disagreed on seed 1: one graded pass at 37s, one
// graded failure at 43s, same prompt and same workspace. A server batching
// requests reduces in whatever order the batch arrives, and the arithmetic
// moves under you regardless of the seed.
//
// So the seed makes runs comparable, not identical: it removes one source of
// variance and leaves the rest. Read the OK column across a whole pass, and do
// not treat "seed 3 failed" as a reproducer.
func (h *DrossRunner) seedFor(string) *int {
	h.runs++
	s := h.runs
	return &s
}

func (h *DrossRunner) Exec(ctx context.Context, prompt, workdir string) (Metrics, error) {
	opts := []agent.Option{}
	if h.Worker != nil && h.WorkerModel != "" {
		opts = append(opts, agent.WithWorker(h.Worker, h.WorkerModel))
	}
	// A fixed seed per repeat, so run 3 of a task is the same run 3 next time
	// and a table can be compared with the one before it. Varied across repeats
	// rather than pinned to one, because five identical runs would measure a
	// single path very precisely and say nothing about the spread.
	seed := h.seedFor(prompt)
	ag, err := agent.New(agent.Config{
		Name: "dross", Model: h.Model, Workdir: workdir, Seed: seed, Thinking: h.Thinking,
	}, h.Provider, opts...)
	if err != nil {
		return Metrics{}, err
	}
	res, err := ag.Run(ctx, prompt)
	if err != nil {
		return Metrics{}, err
	}
	tokens := res.Usage.InputTokens + res.Usage.OutputTokens
	return Metrics{
		Cost: res.Cost(), Tokens: tokens, Turns: res.Turns,
		ModelTime: res.ModelTime, ToolTime: res.ToolTime,
		HasCost: true, HasSplit: true,
	}, nil
}

// CLIRunner shells out to an external agent CLI in non-interactive mode. args is
// the argv template; the literal token "{prompt}" is replaced with the prompt.
// The command runs with the workspace as its working directory.
//
// JSON, when set, says the CLI's stdout is a single result object in Claude
// Code's shape, so the run reports its own turns, cost and API time instead of
// only a stopwatch reading.
type CLIRunner struct {
	Label string
	Bin   string
	Args  []string
	JSON  bool
}

func (c *CLIRunner) Name() string { return c.Label }

func (c *CLIRunner) Available() bool {
	_, err := exec.LookPath(c.Bin)
	return err == nil
}

func (c *CLIRunner) Exec(ctx context.Context, prompt, workdir string) (Metrics, error) {
	argv := make([]string, len(c.Args))
	for i, a := range c.Args {
		if a == "{prompt}" {
			argv[i] = prompt
		} else {
			argv[i] = a
		}
	}
	cmd := exec.CommandContext(ctx, c.Bin, argv...)
	cmd.Dir = workdir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			err = fmt.Errorf("%w: %s", err, firstLine(msg))
		}
		return Metrics{}, err
	}
	if !c.JSON {
		return Metrics{}, nil
	}
	m, jerr := parseClaudeJSON(stdout.Bytes())
	if jerr != nil {
		// A run that worked but printed something unexpected is still a timed
		// run; only its self-reported numbers are lost.
		return Metrics{}, nil
	}
	return m, nil
}

// claudeResult is the subset of `claude -p --output-format json` this reads.
type claudeResult struct {
	IsError      bool    `json:"is_error"`
	NumTurns     int     `json:"num_turns"`
	DurationAPI  int64   `json:"duration_api_ms"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Usage        struct {
		InputTokens      int64 `json:"input_tokens"`
		OutputTokens     int64 `json:"output_tokens"`
		CacheReadTokens  int64 `json:"cache_read_input_tokens"`
		CacheWriteTokens int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	Result string `json:"result"`
}

func parseClaudeJSON(b []byte) (Metrics, error) {
	var r claudeResult
	if err := json.Unmarshal(bytes.TrimSpace(b), &r); err != nil {
		return Metrics{}, err
	}
	if r.IsError {
		return Metrics{}, fmt.Errorf("%s", firstLine(r.Result))
	}
	u := r.Usage
	return Metrics{
		Cost:      r.TotalCostUSD,
		Tokens:    u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens,
		Turns:     r.NumTurns,
		ModelTime: time.Duration(r.DurationAPI) * time.Millisecond,
		HasCost:   true,
		HasSplit:  r.DurationAPI > 0,
	}, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// DefaultExternalRunners are the comparison targets: Claude Code and Codex, in
// their non-interactive modes.
//
// permission is Claude Code's --permission-mode, and it defaults to
// bypassPermissions because anything less is not the same job.
//
// Headless Claude cannot prompt, so under the default mode every edit is
// refused and the run finishes fast and wrong. acceptEdits fixes that half and
// leaves the other: it lets Claude edit but not run commands, so on any task
// whose prompt says the tests fail it cannot run the test that defines the
// work, while dross's runner can. That is a standing bias in dross's favour in
// every row, not a one-off — its permission_denials for `go build` are visible
// in the CLI's own JSON.
//
// The cost of the default is real and worth stating: it lets a model run
// arbitrary commands unattended. It runs in a throwaway workspace, and a
// benchmark that flatters the tool that wrote it is worth less than that risk.
func DefaultExternalRunners(permission string) []Runner {
	if permission == "" {
		permission = "bypassPermissions"
	}
	return []Runner{
		&CLIRunner{
			Label: "claude-code",
			Bin:   "claude",
			Args:  []string{"-p", "--output-format", "json", "--permission-mode", permission, "{prompt}"},
			JSON:  true,
		},
		// --sandbox workspace-write is what --full-auto used to mean: edit and
		// run inside the workspace, no approval prompts. The old flag was
		// removed, and the row it produced was five failures in ten
		// milliseconds — "unexpected argument '--full-auto' found", a harness
		// defect wearing a benchmark result's clothes. The report's
		// never-reached-a-model note is what caught it, twice: the first repair
		// still failed in twenty, on "Not inside a trusted directory", because
		// a bench workspace is a bare temp directory and not a git repository.
		// Hence --skip-git-repo-check, which is exactly that condition waived.
		&CLIRunner{Label: "codex", Bin: "codex",
			Args: []string{"exec", "--sandbox", "workspace-write", "--skip-git-repo-check", "{prompt}"}},
	}
}
