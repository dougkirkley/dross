package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"github.com/dougkirkley/holocron/internal/agent"
	"github.com/dougkirkley/holocron/internal/provider"
	"github.com/dougkirkley/holocron/internal/relay"
	"github.com/dougkirkley/holocron/internal/tui"
)

func newTUICmd() *cobra.Command {
	var (
		render, welcome, running, ask, sessions, shortcuts bool
		subagents, subagentFocus, shells, bang             bool
		models, slash, usage                               bool
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
				// --render pipes to a file (not a TTY), so lipgloss would strip
				// to 16 colors. Force truecolor so screenshots show real hues.
				lipgloss.SetColorProfile(termenv.TrueColor)
				switch {
				case welcome:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoWelcomeView(width, height, logo))
				case running:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoRunningView(width, height))
				case subagents:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoSubagentsView(width, height))
				case shells:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoShellsView(width, height))
				case bang:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoBangView(width, height))
				case models:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoModelsView(width, height))
				case slash:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoSlashView(width, height))
				case usage:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoUsageView(width, height))
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
			// Mutable lead-provider state. Empty when nothing is configured —
			// there is no default provider; the user sets one up via /model.
			st := &leadState{
				name:    res.Lead.Provider,
				baseURL: res.Lead.BaseURL,
				apiKey:  res.Lead.APIKey,
				model:   res.Lead.Model,
			}
			if st.name != "" {
				// A bad stored config shouldn't block the TUI; leave prov nil and
				// let /model reconfigure.
				st.prov, _ = provider.New(providerImpl(st.name), provider.Config{BaseURL: st.baseURL, APIKey: st.apiKey})
			}
			cfgPath, _ := cmd.Flags().GetString("config")

			var prog *tea.Program
			run := func(ctx context.Context, prompt string, emit func(agent.Event)) (*agent.Result, error) {
				if st.prov == nil || st.model == "" {
					return nil, fmt.Errorf("no model configured — use /model to set up a provider")
				}
				opts := []agent.Option{agent.WithEventHook(emit)}
				if prog != nil {
					opts = append(opts, agent.WithAsker(func(req agent.AskRequest) (string, error) {
						return tui.Ask(prog, req)
					}))
				}
				ag, err := agent.New(agent.Config{Name: "holocron", Model: st.model, Workdir: workdir},
					st.prov, opts...)
				if err != nil {
					return nil, err
				}
				return ag.Run(ctx, prompt)
			}

			if once != "" {
				fmt.Fprint(cmd.OutOrStdout(), tui.RenderRun(context.Background(), width, height, "holocron", st.model, st.name, run, once))
				return nil
			}
			mdl := tui.New("holocron", st.model, st.name, run)
			mdl.SetPhrases(res.File.Phrases)
			// /model: list the configured provider's models, set up a provider,
			// and switch models. run() reads st, so updates take effect next turn.
			mdl.SetModelPicker(
				func() []tui.ModelGroup {
					if st.prov == nil {
						return nil
					}
					return []tui.ModelGroup{{Provider: st.name, Models: fetchModelIDs(st.name, st.baseURL, st.apiKey)}}
				},
				func(kind, base, key string) ([]tui.ModelGroup, error) {
					if kind == "ollama" && base == "" {
						base = "http://localhost:11434/v1"
					}
					p, err := provider.New(providerImpl(kind), provider.Config{BaseURL: base, APIKey: key})
					if err != nil {
						return nil, err
					}
					ids := fetchModelIDs(kind, base, key)
					if len(ids) == 0 {
						return nil, fmt.Errorf("no models from %s%s", kind, baseNote(base))
					}
					st.name, st.baseURL, st.apiKey, st.prov = kind, base, key, p
					_ = saveLeadProvider(cfgPath, st)
					return []tui.ModelGroup{{Provider: kind, Models: ids}}, nil
				},
				func(id string) { st.model = id; _ = saveLeadProvider(cfgPath, st) },
			)
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
						if st.prov == nil {
							return fmt.Errorf("no provider — use /model to set one up first")
						}
						return relay.RunDevice(ctx, relay.DeviceOptions{
							Server: server, Token: token, Name: name, Workdir: abs, Repo: repoLabel(workdir),
							Lead: st.prov, LeadModel: st.model, Worker: worker, WorkerModel: res.Worker.Model,
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
	f.BoolVar(&bang, "bang", false, "with --render, show the ! shell-mode input")
	f.BoolVar(&models, "models", false, "with --render, show the /model picker")
	f.BoolVar(&slash, "slash", false, "with --render, show slash-command autocomplete")
	f.BoolVar(&usage, "usage", false, "with --render, show the /usage output")
	f.BoolVar(&subagentFocus, "subagent-focus", false, "with --render, show a selected subagent's detail")
	f.BoolVar(&ask, "ask", false, "with --render, show the ask_user multi-question picker")
	f.BoolVar(&sessions, "sessions", false, "with --render, show the session switcher")
	f.BoolVar(&shortcuts, "shortcuts", false, "with --render, show the shortcuts overlay")
	f.IntVar(&logo, "logo", 0, "logo variant: 0 filled, 1 gradient, 2 boxed, 3 wordmark")
	f.StringVar(&once, "once", "", "run one real prompt, print the final frame, and exit")
	f.IntVar(&width, "width", 96, "width for --render/--once")
	f.IntVar(&height, "height", 28, "height for --render/--once")
	f.StringVarP(&workdir, "workdir", "C", ".", "workspace root")
	f.StringVar(&model, "model", "", "model (default: none — set up via /model)")
	f.StringVar(&provName, "provider", "", "provider (default: none — set up via /model)")
	f.StringVar(&baseURL, "base-url", "", "provider base URL")
	f.StringVar(&apiKey, "api-key", "", "provider API key")
	f.StringVar(&server, "server", "", "holocron server for /login and /rc device linking")
	f.StringVar(&workerProv, "worker-provider", "openai", "worker backend (for /rc)")
	f.StringVar(&workerModel, "worker-model", "", "worker model id (enables delegation over /rc)")
	f.StringVar(&workerBU, "worker-base-url", "", "worker provider base URL")
	return cmd
}
