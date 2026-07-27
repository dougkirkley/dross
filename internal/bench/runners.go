package bench

import (
	"context"
	"os/exec"

	"github.com/dougkirkley/holocron/internal/agent"
	"github.com/dougkirkley/holocron/internal/provider"
)

// HolocronRunner runs tasks with the in-process agent, so it reports real cost.
type HolocronRunner struct {
	Provider    provider.Provider
	Model       string
	Worker      provider.Provider // optional
	WorkerModel string
}

func (h *HolocronRunner) Name() string    { return "holocron" }
func (h *HolocronRunner) Available() bool { return h.Provider != nil }

func (h *HolocronRunner) Exec(ctx context.Context, prompt, workdir string) (Metrics, error) {
	opts := []agent.Option{}
	if h.Worker != nil && h.WorkerModel != "" {
		opts = append(opts, agent.WithWorker(h.Worker, h.WorkerModel))
	}
	ag, err := agent.New(agent.Config{Name: "holocron", Model: h.Model, Workdir: workdir}, h.Provider, opts...)
	if err != nil {
		return Metrics{}, err
	}
	res, err := ag.Run(ctx, prompt)
	if err != nil {
		return Metrics{}, err
	}
	tokens := res.Usage.InputTokens + res.Usage.OutputTokens
	return Metrics{Cost: res.Cost(), Tokens: tokens, HasCost: true}, nil
}

// CLIRunner shells out to an external agent CLI in non-interactive mode. args is
// the argv template; the literal token "{prompt}" is replaced with the prompt.
// The command runs with the workspace as its working directory.
//
// Examples:
//
//	claude: bin="claude", args=["-p", "{prompt}"]
//	codex:  bin="codex",  args=["exec", "{prompt}"]
type CLIRunner struct {
	Label string
	Bin   string
	Args  []string
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
	// External CLIs don't expose token/cost, so metrics are timing/success only.
	return Metrics{}, cmd.Run()
}

// DefaultExternalRunners are the comparison targets: Claude Code and Codex, in
// their non-interactive modes.
func DefaultExternalRunners() []Runner {
	return []Runner{
		&CLIRunner{Label: "claude-code", Bin: "claude", Args: []string{"-p", "{prompt}"}},
		&CLIRunner{Label: "codex", Bin: "codex", Args: []string{"exec", "{prompt}"}},
	}
}
