package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/dross/internal/bench"
	"github.com/dougkirkley/dross/internal/config"
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
	// thinking is nil unless the spec said so, so a row that does not mention
	// it is the model's own default — which is what makes thinking=false vs
	// thinking=true two comparable rows in one table.
	thinking *bool
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
		case "thinking":
			b := v == "true" || v == "1" || v == "yes"
			if !b && v != "false" && v != "0" && v != "no" {
				return d, fmt.Errorf("thinking must be true or false, got %q", v)
			}
			d.thinking = &b
		default:
			return d, fmt.Errorf("unknown key %q (want provider, model, base_url, api_key, worker_model, label, thinking)", k)
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
		if d.thinking != nil {
			// Same model with and without deliberation is the comparison this
			// key exists for, so the two must not share a row.
			if *d.thinking {
				d.label += "+think"
			} else {
				d.label += "-think"
			}
		}
	}
	return d, nil
}

// endpoint resolves where this spec's model actually lives: what the spec says,
// and the environment for whatever it left out.
func (d drossSpec) endpoint() (baseURL, apiKey string) {
	baseURL, apiKey = d.baseURL, d.apiKey
	envBase, envKey := config.EnvFor(d.provider)
	if baseURL == "" {
		baseURL = envBase
	}
	if apiKey == "" {
		apiKey = envKey
	}
	return baseURL, apiKey
}

func (d drossSpec) runner() (bench.Runner, error) {
	// Fall back to the environment the rest of dross reads, so a benchmark and a
	// session point at the same endpoint by default.
	//
	// They did not. Everywhere else — the /model picker, resolveProviders —
	// OPENAI_BASE_URL is how you aim an OpenAI-compatible client at something
	// that is not OpenAI, and only here was it skipped. A spec that left
	// base_url out went to api.openai.com with no key and came back 401 on
	// every task: seven rows of a failure that looks like the agent's and is
	// the harness's.
	base, key := d.endpoint()
	prov, err := provider.New(d.provider, provider.Config{BaseURL: base, APIKey: key})
	if err != nil {
		return nil, fmt.Errorf("provider %s: %w", d.provider, err)
	}
	r := &bench.DrossRunner{Label: d.label, Provider: prov, Model: d.model, Thinking: d.thinking}
	if d.workerModel != "" {
		r.Worker, r.WorkerModel = prov, d.workerModel
	}
	return r, nil
}

func newBenchCmd() *cobra.Command {
	var provName, baseURL, apiKey, model, workerModel, claudePerm string
	var specs, only []string
	var external, keep bool
	var repeat int
	var timeout time.Duration
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
			tasks, err := bench.Only(bench.DefaultTasks(), only)
			if err != nil {
				return err
			}
			bench.KeepWorkspaces = keep
			bench.Timeout = timeout
			fmt.Fprintf(out, "running %d tasks across %d runners, %d times each...\n\n",
				len(tasks), len(runners), repeat)
			// Streamed, because the table cannot exist until the last run is
			// done and that can be an hour away.
			results := bench.RunWith(context.Background(), tasks, runners, repeat,
				bench.StreamHooks(out))
			fmt.Fprint(out, bench.Report(results))
			// The comparison the table can only imply: same prompt, same task,
			// did the two end up in the same place.
			if external && len(runners) > 0 {
				fmt.Fprint(out, bench.Parity(results, runners[0].Name(), "claude-code"))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&specs, "dross", nil,
		"a dross configuration to benchmark, as key=value pairs (provider=,model=,base_url=,api_key=,worker_model=,label=,thinking=); repeatable")
	f.StringVar(&provName, "provider", "openai", "dross lead backend (ignored when --dross is given)")
	f.StringVar(&baseURL, "base-url", "", "provider base URL")
	f.StringVar(&apiKey, "api-key", "", "provider API key")
	f.StringVar(&model, "model", "qwen3-coder:30b", "dross model")
	f.StringVar(&workerModel, "worker-model", "", "dross worker model (enables delegation)")
	f.BoolVar(&external, "external", true, "also run Claude Code and Codex if installed")
	f.IntVar(&repeat, "repeat", bench.DefaultRepeat,
		"run each (runner, task) pair this many times and report the median; one sample cannot tell an improvement from a lucky roll")
	f.StringArrayVar(&only, "task", nil,
		"run only the named task; repeatable (default: all of them)")
	f.DurationVar(&timeout, "timeout", bench.TaskTimeout,
		"give up on a single run after this long; raise it for a slow local model, where a kill at the ceiling reads as a failure the agent did not have")
	f.BoolVar(&keep, "keep", false,
		"leave each run's workspace on disk and print its path, to see what the agent actually did")
	f.StringVar(&claudePerm, "claude-permission", "bypassPermissions",
		"Claude Code --permission-mode for the comparison run; the default lets it build and test, like dross does, so the two are doing the same job")
	return cmd
}
