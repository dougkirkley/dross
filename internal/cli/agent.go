package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/holocron/internal/agent"
	"github.com/dougkirkley/holocron/internal/provider"
	"github.com/dougkirkley/holocron/internal/session"
	"github.com/dougkirkley/holocron/internal/storage"
)

func newAgentCmd() *cobra.Command {
	var (
		workdir, model, name, identity, style             string
		provName, baseURL, apiKey                         string
		workerProv, workerModel, workerBaseURL, workerKey string
		sessionName                                       string
		fresh                                             bool
	)
	cmd := &cobra.Command{
		Use:   "agent [flags] <prompt...>",
		Short: "Run the agent once against a workspace",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.TrimSpace(strings.Join(args, " "))
			if prompt == "" {
				return fmt.Errorf("a prompt is required")
			}

			prov, err := provider.New(provName, provider.Config{BaseURL: baseURL, APIKey: apiKey})
			if err != nil {
				return fmt.Errorf("provider: %w", err)
			}

			opts := []agent.Option{agent.WithOutput(cmd.OutOrStdout())}
			if workerModel != "" {
				wp, err := provider.New(workerProv, provider.Config{BaseURL: workerBaseURL, APIKey: workerKey})
				if err != nil {
					return fmt.Errorf("worker provider: %w", err)
				}
				opts = append(opts, agent.WithWorker(wp, workerModel))
			}
			if sessionName != "" {
				sp, err := mustStorePath()
				if err != nil {
					return err
				}
				store := storage.NewSessionStore(sp)
				if !fresh {
					if b, ok, err := session.Load(store, sessionName); err != nil {
						return fmt.Errorf("load session: %w", err)
					} else if ok {
						fmt.Fprintf(cmd.OutOrStdout(), "resuming session %q (updated %s)\n", sessionName, b.Updated.Format("2006-01-02 15:04"))
						opts = append(opts, agent.WithResume(b.Content))
					}
				}
				n := sessionName
				opts = append(opts, agent.WithCheckpoint(func(brief string) error {
					return session.Save(store, n, brief)
				}))
			}

			ag, err := agent.New(agent.Config{
				Name: name, Identity: identity, Model: model, Workdir: workdir, Style: agent.Style(style),
			}, prov, opts...)
			if err != nil {
				return err
			}
			res, err := ag.Run(context.Background(), prompt)
			if err != nil {
				return fmt.Errorf("run: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "\n--- done in %d turns ---\n", res.Turns)
			for m, u := range res.ByModel {
				fmt.Fprintf(out, "  %-24s in=%d out=%d cache_read=%d  $%.4f\n",
					m, u.InputTokens, u.OutputTokens, u.CacheReadTokens, agent.Cost(m, u))
			}
			fmt.Fprintf(out, "total cost: $%.4f\n", res.Cost())
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&workdir, "workdir", "C", ".", "workspace root")
	f.StringVar(&model, "model", defaultModel, "lead model")
	f.StringVar(&name, "name", "agent", "agent name")
	f.StringVar(&identity, "identity", "", "agent identity / system prompt addendum")
	f.StringVar(&style, "style", "", "output verbosity: normal | terse | caveman")
	f.StringVar(&provName, "provider", "anthropic", "lead backend")
	f.StringVar(&baseURL, "base-url", "", "lead provider base URL")
	f.StringVar(&apiKey, "api-key", "", "lead provider API key")
	f.StringVar(&workerProv, "worker-provider", "openai", "worker backend")
	f.StringVar(&workerModel, "worker-model", "", "worker model id (enables delegation)")
	f.StringVar(&workerBaseURL, "worker-base-url", "", "worker provider base URL")
	f.StringVar(&workerKey, "worker-api-key", "", "worker provider API key")
	f.StringVar(&sessionName, "session", "", "named session: resume from its brief and checkpoint at the end")
	f.BoolVar(&fresh, "fresh", false, "with --session, ignore the existing brief")
	return cmd
}
