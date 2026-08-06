package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/dross/internal/plugins"
	"github.com/dougkirkley/dross/internal/session"
	"github.com/dougkirkley/dross/internal/storage"
	"github.com/dougkirkley/dross/pkg/agent"
	"github.com/dougkirkley/dross/pkg/provider"
)

func newAgentCmd() *cobra.Command {
	var (
		workdir, model, name, identity, style             string
		provName, baseURL, apiKey                         string
		workerProv, workerModel, workerBaseURL, workerKey string
		revProv, revModel, revBaseURL, revKey             string
		compProv, compModel, compBaseURL, compKey         string
		sessionName                                       string
		fresh                                             bool
		maxParallel                                       int
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

			pc, err := resolveProviders(cmd, provName, model)
			if err != nil {
				return err
			}
			model = pc.Lead.Model
			if style == "" && pc.File.Style != "" {
				style = pc.File.Style
			}

			prov, err := provider.New(pc.Lead.Provider, provider.Config{BaseURL: pc.Lead.BaseURL, APIKey: pc.Lead.APIKey})
			if err != nil {
				return fmt.Errorf("provider: %w", err)
			}

			opts := []agent.Option{agent.WithOutput(cmd.OutOrStdout())}
			if pc.WorkerEnabled {
				wp, err := provider.New(pc.Worker.Provider, provider.Config{BaseURL: pc.Worker.BaseURL, APIKey: pc.Worker.APIKey})
				if err != nil {
					return fmt.Errorf("worker provider: %w", err)
				}
				opts = append(opts, agent.WithWorker(wp, pc.Worker.Model))
			}
			mp := maxParallel
			if mp == 0 {
				mp = pc.File.MaxParallel
			}
			if mp > 0 {
				opts = append(opts, agent.WithMaxParallel(mp))
			}
			if pc.ReviewerEnabled {
				rp, err := provider.New(pc.Reviewer.Provider, provider.Config{BaseURL: pc.Reviewer.BaseURL, APIKey: pc.Reviewer.APIKey})
				if err != nil {
					return fmt.Errorf("reviewer provider: %w", err)
				}
				opts = append(opts, agent.WithReviewer(rp, pc.Reviewer.Model))
			}
			if pc.CompactorEnabled {
				cp, err := provider.New(pc.Compactor.Provider, provider.Config{BaseURL: pc.Compactor.BaseURL, APIKey: pc.Compactor.APIKey})
				if err != nil {
					return fmt.Errorf("compactor provider: %w", err)
				}
				opts = append(opts, agent.WithCompactor(cp, pc.Compactor.Model, pc.CompactAt))
				if p, err := compactStatsPath(); err == nil {
					opts = append(opts, agent.WithCompactStats(newCompactRecorder(p)))
				}
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

			// Skills (SKILL.md / plugins), model-invocable via the `skill` tool.
			var skillSpecs []agent.SkillSpec
			for _, s := range plugins.LoadAll(workdir) {
				if !s.DisableModelInvocation {
					skillSpecs = append(skillSpecs, agent.SkillSpec{Name: s.Name, Description: s.Description, Body: s.Body})
				}
			}
			if len(skillSpecs) > 0 {
				opts = append(opts, agent.WithSkills(skillSpecs))
			}
			cfgPath, _ := cmd.Flags().GetString("config")
			opts = append(opts, agent.WithTool(configToolDef(), configToolHandler(cfgPath)))
			if specs := mergedMCP(pc.File, workdir); len(specs) > 0 {
				opts = append(opts, agent.WithMCPServers(specs))
			}

			ag, err := agent.New(agent.Config{
				Name: name, Identity: identity, Model: model, Workdir: workdir, Style: agent.Style(style), MaxTurns: pc.File.MaxTurns,
			}, prov, opts...)
			if err != nil {
				return err
			}
			defer ag.Close()
			res, err := ag.Run(context.Background(), prompt)
			if err != nil {
				return fmt.Errorf("run: %w", err)
			}

			// This command prints the review and then exits, so it has to wait
			// for it. Everything above it is already known.
			ag.Wait()

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "\n--- done in %d turns ---\n", res.Turns)
			for m, u := range res.ByModel {
				fmt.Fprintf(out, "  %-24s in=%d out=%d cache_read=%d  $%.4f\n",
					m, u.InputTokens, u.OutputTokens, u.CacheReadTokens, agent.Cost(m, u))
			}
			fmt.Fprintf(out, "total cost: $%.4f\n", res.Cost())
			if res.Review != "" {
				fmt.Fprintf(out, "\n--- code review (%s) ---\n%s\n", pc.Reviewer.Model, res.Review)
			}
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
	f.StringVar(&revProv, "reviewer-provider", "anthropic", "reviewer backend")
	f.StringVar(&revModel, "reviewer-model", "", "reviewer model id (enables the code-review skill)")
	f.StringVar(&revBaseURL, "reviewer-base-url", "", "reviewer provider base URL")
	f.StringVar(&revKey, "reviewer-api-key", "", "reviewer provider API key")
	f.StringVar(&compProv, "compactor-provider", "openai", "compactor backend")
	f.StringVar(&compModel, "compactor-model", "", "compactor model id (enables the token-efficiency skill)")
	f.StringVar(&compBaseURL, "compactor-base-url", "", "compactor provider base URL")
	f.StringVar(&compKey, "compactor-api-key", "", "compactor provider API key")
	f.IntVar(&maxParallel, "max-parallel", 0, "max concurrent worker subagents for batch delegation (default 2)")
	f.StringVar(&sessionName, "session", "", "named session: resume from its brief and checkpoint at the end")
	f.BoolVar(&fresh, "fresh", false, "with --session, ignore the existing brief")
	return cmd
}
