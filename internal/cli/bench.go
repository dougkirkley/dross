package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/dross/internal/bench"
	"github.com/dougkirkley/dross/pkg/provider"
)

// drossSpec is one dross configuration to benchmark, written as a
// comma-separated key=value list:
//
//	provider=openai,model=gpt-oss:20b,base_url=https://host/v1,label=ollama
//
// Repeatable, because the comparison that matters needs more than one dross in
// the table at a time: dross on a local model against dross on Claude isolates
// the model, and dross on Claude against native Claude Code isolates the agent.
// A single --provider flag could only ever show one of those.
type drossSpec struct {
	provider, model, baseURL, apiKey, workerModel, label string
}

func parseDrossSpec(s string) (drossSpec, error) {
	var d drossSpec
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return d, fmt.Errorf("%q is not key=value", part)
		}
		switch strings.TrimSpace(k) {
		case "provider":
			d.provider = v
		case "model":
			d.model = v
		case "base_url":
			d.baseURL = v
		case "api_key":
			d.apiKey = v
		case "worker_model":
			d.workerModel = v
		case "label":
			d.label = v
		default:
			return d, fmt.Errorf("unknown key %q (want provider, model, base_url, api_key, worker_model, label)", k)
		}
	}
	if d.provider == "" {
		return d, fmt.Errorf("spec needs a provider=")
	}
	if d.label == "" {
		// Readable by default, and unique per configuration, so two rows are
		// never silently merged into one.
		d.label = "dross/" + d.provider
		if d.model != "" {
			d.label += ":" + d.model
		}
		if d.workerModel != "" {
			// A lead with a worker is a different agent from the same lead
			// alone — that is the whole point of measuring it — so the rows
			// must not collapse into one.
			d.label += "+" + d.workerModel
		}
	}
	return d, nil
}

func (d drossSpec) runner() (bench.Runner, error) {
	prov, err := provider.New(d.provider, provider.Config{BaseURL: d.baseURL, APIKey: d.apiKey})
	if err != nil {
		return nil, fmt.Errorf("provider %s: %w", d.provider, err)
	}
	r := &bench.DrossRunner{Label: d.label, Provider: prov, Model: d.model}
	if d.workerModel != "" {
		r.Worker, r.WorkerModel = prov, d.workerModel
	}
	return r, nil
}

func newBenchCmd() *cobra.Command {
	var provName, baseURL, apiKey, model, workerModel, claudePerm string
	var specs []string
	var external bool
	var repeat int
	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Benchmark dross against Claude Code and Codex on coding tasks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(specs) == 0 {
				// No spec given: fall back to the single-configuration flags.
				specs = []string{fmt.Sprintf("provider=%s,model=%s,base_url=%s,api_key=%s,worker_model=%s",
					provName, model, baseURL, apiKey, workerModel)}
			}
			var runners []bench.Runner
			for _, s := range specs {
				spec, err := parseDrossSpec(s)
				if err != nil {
					return fmt.Errorf("--dross %q: %w", s, err)
				}
				r, err := spec.runner()
				if err != nil {
					return err
				}
				runners = append(runners, r)
			}
			if external {
				runners = append(runners, bench.DefaultExternalRunners(claudePerm)...)
			}
			out := cmd.OutOrStdout()
			tasks := bench.DefaultTasks()
			fmt.Fprintf(out, "running %d tasks across %d runners, %d times each...\n\n",
				len(tasks), len(runners), repeat)
			results := bench.Run(context.Background(), tasks, runners, repeat)
			fmt.Fprint(out, bench.Report(results))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&specs, "dross", nil,
		"a dross configuration to benchmark, as key=value pairs (provider=,model=,base_url=,api_key=,worker_model=,label=); repeatable")
	f.StringVar(&provName, "provider", "openai", "dross lead backend (ignored when --dross is given)")
	f.StringVar(&baseURL, "base-url", "", "provider base URL")
	f.StringVar(&apiKey, "api-key", "", "provider API key")
	f.StringVar(&model, "model", "qwen3-coder:30b", "dross model")
	f.StringVar(&workerModel, "worker-model", "", "dross worker model (enables delegation)")
	f.BoolVar(&external, "external", true, "also run Claude Code and Codex if installed")
	f.IntVar(&repeat, "repeat", bench.DefaultRepeat,
		"run each (runner, task) pair this many times and report the median; one sample cannot tell an improvement from a lucky roll")
	f.StringVar(&claudePerm, "claude-permission", "acceptEdits",
		"Claude Code --permission-mode for the comparison run; bypassPermissions also lets it build and test, like dross does")
	return cmd
}
