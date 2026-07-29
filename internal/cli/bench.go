package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/ergon/internal/bench"
	"github.com/dougkirkley/ergon/internal/provider"
)

func newBenchCmd() *cobra.Command {
	var provName, baseURL, apiKey, model, workerModel string
	var external bool
	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Benchmark ergon against Claude Code and Codex on coding tasks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			prov, err := provider.New(provName, provider.Config{BaseURL: baseURL, APIKey: apiKey})
			if err != nil {
				return fmt.Errorf("provider: %w", err)
			}
			runners := []bench.Runner{&bench.ErgonRunner{
				Provider: prov, Model: model, Worker: prov, WorkerModel: workerModel,
			}}
			if external {
				runners = append(runners, bench.DefaultExternalRunners()...)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "running %d tasks across %d runners...\n\n", len(bench.DefaultTasks()), len(runners))
			results := bench.Run(context.Background(), bench.DefaultTasks(), runners)
			fmt.Fprint(out, bench.Report(results))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&provName, "provider", "openai", "ergon lead backend")
	f.StringVar(&baseURL, "base-url", "", "provider base URL")
	f.StringVar(&apiKey, "api-key", "", "provider API key")
	f.StringVar(&model, "model", "qwen3-coder:30b", "ergon model")
	f.StringVar(&workerModel, "worker-model", "", "ergon worker model (enables delegation)")
	f.BoolVar(&external, "external", true, "also run Claude Code and Codex if installed")
	return cmd
}
