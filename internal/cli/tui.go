package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/dougkirkley/holocron/internal/agent"
	"github.com/dougkirkley/holocron/internal/provider"
	"github.com/dougkirkley/holocron/internal/relay"
	"github.com/dougkirkley/holocron/internal/tui"
)

func newTUICmd() *cobra.Command {
	var (
		render, welcome, running, ask, sessions, shortcuts bool
		subagents, subagentFocus, shells                   bool
		logo, width, height                                int
		once                                               string
		workdir, model, provName                           string
		baseURL, apiKey, server                            string
		workerProv, workerModel, workerBU                  string
	)
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal client",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if render {
				switch {
				case welcome:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoWelcomeView(width, height, logo))
				case running:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoRunningView(width, height))
				case subagents:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoSubagentsView(width, height))
				case shells:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoShellsView(width, height))
				case subagentFocus:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoSubagentFocusView(width, height))
				case ask:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoAskView(width, height))
				case sessions:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoSessionsView(width, height))
				case shortcuts:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoHelpView(width, height))
				default:
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
			var prog *tea.Program
			run := func(ctx context.Context, prompt string, emit func(agent.Event)) (*agent.Result, error) {
				opts := []agent.Option{agent.WithEventHook(emit)}
				if prog != nil {
					opts = append(opts, agent.WithAsker(func(req agent.AskRequest) (string, error) {
						return tui.Ask(prog, req)
					}))
				}
				ag, err := agent.New(agent.Config{Name: "holocron", Model: model, Workdir: workdir},
					prov, opts...)
				if err != nil {
					return nil, err
				}
				return ag.Run(ctx, prompt)
			}

			if once != "" {
				fmt.Fprint(cmd.OutOrStdout(), tui.RenderRun(context.Background(), width, height, "holocron", model, provName, run, once))
				return nil
			}
			mdl := tui.New("holocron", model, provName, run)
			mdl.SetPhrases(res.File.Phrases)
			// Wire /login + /rc when a server is configured, so this TUI can
			// advertise the machine as drivable by the logged-in SSO user.
			if server != "" {
				var worker provider.Provider
				if res.WorkerEnabled {
					worker, _ = provider.New(res.Worker.Provider, provider.Config{BaseURL: res.Worker.BaseURL, APIKey: res.Worker.APIKey})
				}
				name, _ := os.Hostname()
				if name == "" {
					name = "device"
				}
				abs, _ := filepath.Abs(workdir)
				mdl.SetRemote(server,
					func(ctx context.Context) error {
						_, err := relay.Login(ctx, server)
						return err
					},
					func(ctx context.Context, logf func(string)) error {
						token := relay.LoadToken(server)
						if token == "" {
							var err error
							if token, err = relay.Login(ctx, server); err != nil {
								return err
							}
						}
						return relay.RunDevice(ctx, relay.DeviceOptions{
							Server: server, Token: token, Name: name, Workdir: abs, Repo: repoLabel(workdir),
							Lead: prov, LeadModel: model, Worker: worker, WorkerModel: res.Worker.Model,
							Log: logf,
						})
					})
			}
			prog = tea.NewProgram(mdl, tea.WithAltScreen())
			_, err = prog.Run()
			return err
		},
	}
	f := cmd.Flags()
	f.BoolVar(&render, "render", false, "print a populated demo view and exit (no model)")
	f.BoolVar(&welcome, "welcome", false, "with --render, show the empty-state logo splash")
	f.BoolVar(&running, "running", false, "with --render, show a mid-run frame (spinner, phrase, tokens)")
	f.BoolVar(&subagents, "subagents", false, "with --render, show the live subagent tree")
	f.BoolVar(&shells, "shells", false, "with --render, show background shells")
	f.BoolVar(&subagentFocus, "subagent-focus", false, "with --render, show a selected subagent's detail")
	f.BoolVar(&ask, "ask", false, "with --render, show the ask_user multi-question picker")
	f.BoolVar(&sessions, "sessions", false, "with --render, show the session switcher")
	f.BoolVar(&shortcuts, "shortcuts", false, "with --render, show the shortcuts overlay")
	f.IntVar(&logo, "logo", 0, "logo variant: 0 filled, 1 gradient, 2 boxed, 3 wordmark")
	f.StringVar(&once, "once", "", "run one real prompt, print the final frame, and exit")
	f.IntVar(&width, "width", 96, "width for --render/--once")
	f.IntVar(&height, "height", 28, "height for --render/--once")
	f.StringVarP(&workdir, "workdir", "C", ".", "workspace root")
	f.StringVar(&model, "model", defaultModel, "model")
	f.StringVar(&provName, "provider", "anthropic", "provider")
	f.StringVar(&baseURL, "base-url", "", "provider base URL")
	f.StringVar(&apiKey, "api-key", "", "provider API key")
	f.StringVar(&server, "server", "", "holocron server for /login and /rc device linking")
	f.StringVar(&workerProv, "worker-provider", "openai", "worker backend (for /rc)")
	f.StringVar(&workerModel, "worker-model", "", "worker model id (enables delegation over /rc)")
	f.StringVar(&workerBU, "worker-base-url", "", "worker provider base URL")
	return cmd
}
