package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"github.com/dougkirkley/dross/internal/catalog"
	"github.com/dougkirkley/dross/internal/plugins"
	"github.com/dougkirkley/dross/internal/relay"
	"github.com/dougkirkley/dross/internal/session"
	"github.com/dougkirkley/dross/internal/storage"
	"github.com/dougkirkley/dross/internal/tui"
	"github.com/dougkirkley/dross/pkg/agent"
	"github.com/dougkirkley/dross/pkg/provider"
	"github.com/dougkirkley/dross/pkg/provider/googleauth"
)

func newTUICmd() *cobra.Command {
	var (
		render, welcome, running, ask, sessions, shortcuts bool
		shimmer                                            int
		subagents, subagentFocus, shells, bang             bool
		models, slash, usage, toolsv, provs, skillsv       bool
		pluginv, themev                                    bool
		logo, width, height                                int
		once, theme                                        string
		workdir, model, provName                           string
		baseURL, apiKey, server, resume                    string
		workerProv, workerModel, workerBU                  string
	)
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal client",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if theme != "" {
				tui.SetTheme(theme) // flag overrides the config theme
			}
			if render {
				// --render pipes to a file (not a TTY), so lipgloss would strip
				// to 16 colors. Force truecolor so screenshots show real hues.
				lipgloss.SetColorProfile(termenv.TrueColor)
				tui.SetDemoShimmer(shimmer)
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
				case provs:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoProvidersView(width, height))
				case slash:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoSlashView(width, height))
				case usage:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoUsageView(width, height))
				case toolsv:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoToolsView(width, height))
				case skillsv:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoSkillsView(width, height))
				case themev:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoThemeView(width, height))
				case pluginv:
					fmt.Fprint(cmd.OutOrStdout(), tui.DemoPluginView(width, height, logo))
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
			if res.WorkerEnabled && res.Worker.Model != "" {
				st.workerName = res.Worker.Provider
				st.workerBaseURL = res.Worker.BaseURL
				st.workerAPIKey = res.Worker.APIKey
				st.workerModel = res.Worker.Model
				st.workerProv, _ = provider.New(providerImpl(st.workerName), provider.Config{BaseURL: st.workerBaseURL, APIKey: st.workerAPIKey})
			}
			cfgPath, _ := cmd.Flags().GetString("config")
			// Agent identity: the configured identity prompt, then project context
			// files (AGENTS.md / CLAUDE.md).
			identity := loadProjectContext(workdir)
			if ip := res.File.IdentityPrompt; ip != "" {
				if identity != "" {
					identity = ip + "\n\n" + identity
				} else {
					identity = ip
				}
			}

			var prog *tea.Program
			// Skills + slash commands (Claude-Code-compatible), reloadable after a
			// /plugin change. skillSpecs feeds the agent; skillItems the TUI.
			var skillSpecs []agent.SkillSpec
			var skillItems []tui.SkillItem
			var skillGen int
			skillOff := map[string]bool{} // skills disabled via the /skills modal
			loadSkills := func() {
				skillSpecs, skillItems = skillSpecs[:0], skillItems[:0]
				for _, s := range plugins.LoadAll(workdir) {
					skillItems = append(skillItems, tui.SkillItem{
						Name: s.Name, Desc: s.Description, Body: s.Body,
						Source: s.Source, UserInvocable: s.UserInvocable,
					})
					if !s.DisableModelInvocation && !skillOff[s.Name] {
						skillSpecs = append(skillSpecs, agent.SkillSpec{Name: s.Name, Description: s.Description, Body: s.Body})
					}
				}
			}
			loadSkills()

			// Session persistence for resume: a handoff brief is saved under a
			// session id on exit, and --resume <id> seeds a new session from it.
			var store *storage.SessionStore
			if sp, err := mustStorePath(); err == nil {
				store = storage.NewSessionStore(sp)
			}
			sessionID := resume
			if sessionID == "" {
				sessionID = newSessionID()
			}
			resumeBrief := ""
			if resume != "" && store != nil {
				if b, ok, _ := session.Load(store, resume); ok {
					resumeBrief = b.Content
				}
			}

			// One persistent agent per (provider, model, skills) so the conversation
			// is remembered across prompts; rebuilt when any of those change.
			var conv *agent.Agent
			var convKey string
			run := func(ctx context.Context, prompt, mode string, emit func(agent.Event)) (*agent.Result, error) {
				if st.prov == nil || st.model == "" {
					return nil, fmt.Errorf("no model configured — use /model to set up a provider")
				}
				key := fmt.Sprintf("%s|%s|%s|%s|%d", st.name, st.model, st.workerName, st.workerModel, skillGen)
				if conv == nil || key != convKey {
					opts := []agent.Option{agent.WithConversation()}
					opts = append(opts, agent.WithTool(configToolDef(), configToolHandler(cfgPath)))
					if store != nil {
						opts = append(opts, agent.WithCheckpointSaver(func(brief string) error {
							return session.Save(store, sessionID, brief)
						}))
					}
					if resumeBrief != "" {
						opts = append(opts, agent.WithResume(resumeBrief))
						resumeBrief = "" // seed only the first session
					}
					if len(skillSpecs) > 0 {
						opts = append(opts, agent.WithSkills(skillSpecs))
					}
					if st.workerProv != nil && st.workerModel != "" {
						opts = append(opts, agent.WithWorker(st.workerProv, st.workerModel))
					}
					if prog != nil {
						opts = append(opts, agent.WithAsker(func(req agent.AskRequest) (string, error) {
							return tui.Ask(prog, req)
						}))
					}
					if len(res.File.Tools) > 0 {
						opts = append(opts, agent.WithAllowedTools(res.File.Tools))
					}
					if len(res.File.Plugins) > 0 {
						specs := make([]agent.PluginSpec, 0, len(res.File.Plugins))
						for _, p := range res.File.Plugins {
							specs = append(specs, agent.PluginSpec{
								Name: p.Name, Description: p.Description, Command: p.Command,
								Properties: p.Properties, Required: p.Required,
							})
						}
						opts = append(opts, agent.WithPluginTools(specs))
					}
					if len(res.File.Permission) > 0 {
						rules := make(map[string]agent.PermRuleSpec, len(res.File.Permission))
						for tool, p := range res.File.Permission {
							rules[tool] = agent.PermRuleSpec{Decision: p.Blanket, Patterns: p.Rules}
						}
						opts = append(opts, agent.WithPermissionRules(rules))
					}
					a, err := agent.New(agent.Config{Name: "dross", Model: st.model, Workdir: workdir, Identity: identity, MaxTurns: res.File.MaxTurns},
						st.prov, opts...)
					if err != nil {
						return nil, err
					}
					conv, convKey = a, key
				}
				conv.SetEventHook(emit)
				conv.SetPermission(mode)
				return conv.Run(ctx, expandMentions(workdir, prompt))
			}

			if once != "" {
				fmt.Fprint(cmd.OutOrStdout(), tui.RenderRun(context.Background(), width, height, "dross", st.model, st.name, run, once))
				return nil
			}
			appName := "dross"
			if res.File.Name != "" {
				appName = res.File.Name
			}
			tui.SetLogoWord(appName)
			mdl := tui.New(appName, st.model, st.name, run)
			absWd, _ := filepath.Abs(workdir)
			mdl.SetMeta(Version, absWd)
			mdl.SetConfigPath(configFilePath(cfgPath))
			if theme == "" && res.File.Theme != "" {
				tui.SetTheme(res.File.Theme)
			}
			if len(res.File.Gradient) > 0 {
				tui.SetGradient(res.File.Gradient)
			}
			if len(res.File.Colors) > 0 {
				tui.ApplyColors(res.File.Colors) // per-role overrides on top of the theme
			}
			mdl.SetThemeSaver(func(name string) {
				_ = setConfigKey(configFilePath(cfgPath), "theme", name)
			})
			mdl.SetWelcomeSaver(func(name string) {
				_ = setConfigKey(configFilePath(cfgPath), "welcome", name)
			})
			if store != nil {
				mdl.SetCheckpoint(func(ctx context.Context) error {
					if conv != nil {
						return conv.Checkpoint(ctx)
					}
					return nil
				})
			}
			mdl.SetWelcomeStyle(welcomeStyleFor(res.File.Welcome))
			mdl.SetPhrases(res.File.Phrases)
			mdl.SetSkillItems(skillItems)
			mdl.SetPluginReload(func() []tui.SkillItem {
				loadSkills()
				skillGen++
				conv = nil // rebuild the agent with the new skill set on the next run
				return skillItems
			})
			mdl.SetSkillsApply(func(off map[string]bool) {
				skillOff = off
				loadSkills()
				skillGen++
				conv = nil // apply the new enabled/disabled set on the next run
			})
			mdl.SetReset(func() {
				if conv != nil {
					conv.ResetConversation()
				}
			})
			mdl.SetBackgrounder(func() {
				if conv != nil {
					conv.BackgroundForeground()
				}
			})
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
					// A known catalog provider fills in the base URL + API-key env.
					impl := providerImpl(kind)
					if c, ok := catalog.Get(kind); ok {
						impl = c.Impl
						if base == "" {
							base = c.BaseURL
						}
						if key == "" && c.APIKeyEnv != "" {
							key = os.Getenv(c.APIKeyEnv)
						}
					}
					p, err := provider.New(impl, provider.Config{BaseURL: base, APIKey: key})
					if err != nil {
						return nil, err
					}
					ids := fetchModelIDs(impl, base, key)
					if len(ids) == 0 {
						return nil, fmt.Errorf("no models from %s%s", kind, baseNote(base))
					}
					st.name, st.baseURL, st.apiKey, st.prov = kind, base, key, p
					_ = saveLeadProvider(cfgPath, st)
					return []tui.ModelGroup{{Provider: kind, Models: ids}}, nil
				},
				func(prov, model string) error {
					// Configure the provider if the pick switched away from the
					// current one (base URL + key from the catalog / env).
					if prov != "" && (prov != st.name || st.prov == nil) {
						impl := providerImpl(prov)
						base, key := st.baseURL, st.apiKey
						if c, ok := catalog.Get(prov); ok {
							impl, base = c.Impl, c.BaseURL
							if c.APIKeyEnv != "" {
								key = os.Getenv(c.APIKeyEnv)
							}
						}
						p, err := provider.New(impl, provider.Config{BaseURL: base, APIKey: key})
						if err != nil {
							// Surface the failure (e.g. missing GEMINI_API_KEY) so the
							// pick is rejected instead of silently leaving the old
							// provider running a mismatched model.
							return err
						}
						st.name, st.baseURL, st.apiKey, st.prov = prov, base, key, p
					}
					st.model = model
					_ = saveLeadProvider(cfgPath, st)
					return nil
				},
			)
			// OAuth-only providers (vertex): if picking one fails for lack of
			// credentials, run the browser login and then apply the model.
			mdl.SetLogin(func(ctx context.Context, _ string) error {
				return googleauth.Login(ctx, io.Discard)
			}, "vertex")
			// Worker slot of the /model picker (the cheaper delegate model).
			mdl.SetWorker(
				func(prov, model string) {
					if prov != "" && (prov != st.workerName || st.workerProv == nil) {
						impl := providerImpl(prov)
						base, key := "", ""
						if prov == st.name { // reuse the lead's endpoint if same provider
							base, key = st.baseURL, st.apiKey
						}
						if c, ok := catalog.Get(prov); ok {
							impl, base = c.Impl, c.BaseURL
							if key == "" && c.APIKeyEnv != "" {
								key = os.Getenv(c.APIKeyEnv)
							}
						}
						if p, err := provider.New(impl, provider.Config{BaseURL: base, APIKey: key}); err == nil {
							st.workerName, st.workerBaseURL, st.workerAPIKey, st.workerProv = prov, base, key, p
						}
					}
					if st.workerProv == nil {
						return // provider failed to build; don't record a phantom worker
					}
					st.workerModel = model
					_ = saveLeadProvider(cfgPath, st)
				},
				st.workerName, st.workerModel,
			)
			// Catalog as provider→models groups for the /model picker.
			var groups []tui.ModelGroup
			for _, id := range catalog.IDs() {
				c, _ := catalog.Get(id)
				groups = append(groups, tui.ModelGroup{Provider: id, Models: c.Models})
			}
			mdl.SetProviders(groups)
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
			// Mouse capture is OFF by default so the terminal's own text
			// selection / copy-paste works; /mouse toggles it on for
			// click-to-expand tool output.
			// Mouse cell-motion on: the wheel scrolls the transcript. Text
			// selection still works with the terminal's modifier (Shift-drag on
			// most terminals, Option-drag in macOS Terminal/iTerm2); /mouse turns
			// capture off for plain drag-select.
			// Mouse cell-motion on: the wheel scrolls, and dross implements its own
			// drag-to-select that auto-copies on release (like Claude Code / Crush),
			// so selecting text copies it without a modifier. /mouse turns capture
			// off to fall back to the terminal's native selection.
			prog = tea.NewProgram(mdl, tea.WithAltScreen(), tea.WithMouseCellMotion())
			if _, err = prog.Run(); err != nil {
				return err
			}
			// If a handoff brief was saved this session, tell the user how to resume.
			if store != nil {
				if _, ok, _ := session.Load(store, sessionID); ok {
					fmt.Fprintf(cmd.OutOrStdout(), "\nResume this session with:\n  dross --resume %s\n", sessionID)
				}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&resume, "resume", "", "resume a session by id (see the hint printed on exit)")
	f.StringVar(&theme, "theme", "", "color theme: crystal|ember|matrix|grape|mono|tokyonight|catppuccin|gruvbox|dracula|nord|onedark|rose-pine|solarized")
	f.BoolVar(&render, "render", false, "print a populated demo view and exit (no model)")
	f.BoolVar(&welcome, "welcome", false, "with --render, show the empty-state logo splash")
	f.BoolVar(&running, "running", false, "with --render, show a mid-run frame (spinner, phrase, tokens)")
	f.BoolVar(&subagents, "subagents", false, "with --render, show the live subagent tree")
	f.BoolVar(&shells, "shells", false, "with --render, show background shells")
	f.BoolVar(&bang, "bang", false, "with --render, show the ! shell-mode input")
	f.BoolVar(&models, "models", false, "with --render, show the /model picker")
	f.BoolVar(&slash, "slash", false, "with --render, show slash-command autocomplete")
	f.BoolVar(&usage, "usage", false, "with --render, show the /usage output")
	f.BoolVar(&toolsv, "tools", false, "with --render, show the /tools viewer")
	f.BoolVar(&skillsv, "skills-view", false, "with --render, show the /skills modal")
	f.BoolVar(&themev, "theme-view", false, "with --render, show the /theme picker")
	f.BoolVar(&pluginv, "plugin-view", false, "with --render, show the /plugin browser (--logo picks the tab)")
	f.BoolVar(&provs, "providers", false, "with --render, show the provider picker")
	f.BoolVar(&subagentFocus, "subagent-focus", false, "with --render, show a selected subagent's detail")
	f.BoolVar(&ask, "ask", false, "with --render, show the ask_user multi-question picker")
	f.BoolVar(&sessions, "sessions", false, "with --render, show the session switcher")
	f.BoolVar(&shortcuts, "shortcuts", false, "with --render, show the shortcuts overlay")
	f.IntVar(&logo, "logo", 0, "logo variant: 0 filled, 1 gradient, 2 boxed, 3 wordmark")
	f.IntVar(&shimmer, "shimmer", 0, "with --render --welcome, shimmer animation phase")
	f.StringVar(&once, "once", "", "run one real prompt, print the final frame, and exit")
	f.IntVar(&width, "width", 96, "width for --render/--once")
	f.IntVar(&height, "height", 28, "height for --render/--once")
	f.StringVarP(&workdir, "workdir", "C", ".", "workspace root")
	f.StringVar(&model, "model", "", "model (default: none — set up via /model)")
	f.StringVar(&provName, "provider", "", "provider (default: none — set up via /model)")
	f.StringVar(&baseURL, "base-url", "", "provider base URL")
	f.StringVar(&apiKey, "api-key", "", "provider API key")
	f.StringVar(&server, "server", "", "dross server for /login and /rc device linking")
	f.StringVar(&workerProv, "worker-provider", "openai", "worker backend (for /rc)")
	f.StringVar(&workerModel, "worker-model", "", "worker model id (enables delegation over /rc)")
	f.StringVar(&workerBU, "worker-base-url", "", "worker provider base URL")
	return cmd
}
