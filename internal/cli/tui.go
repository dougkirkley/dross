package cli

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/dougkirkley/holocron/internal/agent"
	"github.com/dougkirkley/holocron/internal/provider"
	"github.com/dougkirkley/holocron/internal/tui"
)

func newTUICmd() *cobra.Command {
	var (
		render, welcome          bool
		logo, width, height      int
		once                     string
		workdir, model, provName string
		baseURL, apiKey          string
	)
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal client",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if render {
				if welcome {
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoWelcomeView(width, height, logo))
				} else {
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoView(width, height))
				}
				return nil
			}

			res, err := resolveProviders(cmd, provName, model)
			if err != nil {
				return err
			}
			provName, model = res.Lead.Provider, res.Lead.Model
			prov, err := provider.New(provName, provider.Config{BaseURL: res.Lead.BaseURL, APIKey: res.Lead.APIKey})
			if err != nil {
				return fmt.Errorf("provider: %w", err)
			}
			run := func(ctx context.Context, prompt string, emit func(agent.Event)) (*agent.Result, error) {
				ag, err := agent.New(agent.Config{Name: "holocron", Model: model, Workdir: workdir},
					prov, agent.WithEventHook(emit))
				if err != nil {
					return nil, err
				}
				return ag.Run(ctx, prompt)
			}

			if once != "" {
				fmt.Fprint(cmd.OutOrStdout(), tui.RenderRun(context.Background(), width, height, "holocron", model, provName, run, once))
				return nil
			}
			p := tea.NewProgram(tui.New("holocron", model, provName, run), tea.WithAltScreen())
			_, err = p.Run()
			return err
		},
	}
	f := cmd.Flags()
	f.BoolVar(&render, "render", false, "print a populated demo view and exit (no model)")
	f.BoolVar(&welcome, "welcome", false, "with --render, show the empty-state logo splash")
	f.IntVar(&logo, "logo", 0, "logo variant: 0 filled, 1 gradient, 2 boxed, 3 wordmark")
	f.StringVar(&once, "once", "", "run one real prompt, print the final frame, and exit")
	f.IntVar(&width, "width", 96, "width for --render/--once")
	f.IntVar(&height, "height", 28, "height for --render/--once")
	f.StringVarP(&workdir, "workdir", "C", ".", "workspace root")
	f.StringVar(&model, "model", defaultModel, "model")
	f.StringVar(&provName, "provider", "anthropic", "provider")
	f.StringVar(&baseURL, "base-url", "", "provider base URL")
	f.StringVar(&apiKey, "api-key", "", "provider API key")
	return cmd
}
