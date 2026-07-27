// Command holocron is a cost-aware, multi-model coding agent.
//
// Subcommands:
//
//	holocron serve [-addr :8080] [-model claude-opus-5] [-C workdir]
//	holocron agent [-C workdir] [-model claude-opus-5] [-name x] <prompt...>
package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dougkirkley/holocron/internal/agent"
	"github.com/dougkirkley/holocron/internal/artifact"
	"github.com/dougkirkley/holocron/internal/auth"
	"github.com/dougkirkley/holocron/internal/bench"
	"github.com/dougkirkley/holocron/internal/provider"
	"github.com/dougkirkley/holocron/internal/server"
	"github.com/dougkirkley/holocron/internal/session"
	"github.com/dougkirkley/holocron/internal/storage"
	"github.com/dougkirkley/holocron/internal/tui"

	// Register model backends (driver-style; see internal/provider).
	_ "github.com/dougkirkley/holocron/internal/provider/anthropic"
	_ "github.com/dougkirkley/holocron/internal/provider/openai"
)

const defaultModel = "claude-opus-5"

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "serve":
		serveCmd(os.Args[2:])
	case "agent":
		agentCmd(os.Args[2:])
	case "brief":
		briefCmd(os.Args[2:])
	case "tui":
		tuiCmd(os.Args[2:])
	case "bench":
		benchCmd(os.Args[2:])
	default:
		usage()
	}
}

// storePath is where session handoff briefs live.
func storePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Fatalf("config dir: %v", err)
	}
	p := configDir + "/holocron/sessions"
	if err := os.MkdirAll(p, 0o755); err != nil {
		log.Fatalf("storage dir: %v", err)
	}
	return p
}

func usage() {
	fmt.Fprintf(os.Stderr, `holocron — cost-aware multi-model coding agent

usage:
  holocron serve [-addr :8080] [-model %s] [-C dir]
  holocron agent [-C dir] [-model %s] [-session name] [-fresh] <prompt...>
  holocron brief [name]        # print a saved handoff brief, or list sessions

providers registered: %s
`, defaultModel, defaultModel, strings.Join(provider.Names(), ", "))
	os.Exit(2)
}

// tuiCmd launches the terminal client, or renders a static demo view with
// -render (for screenshots; needs no TTY or model).
func tuiCmd(args []string) {
	fs := flag.NewFlagSet("tui", flag.ExitOnError)
	render := fs.Bool("render", false, "print a populated demo view and exit (no model)")
	welcome := fs.Bool("welcome", false, "with -render, show the empty-state logo splash")
	logo := fs.Int("logo", 0, "logo variant: 0 filled, 1 gradient, 2 boxed, 3 wordmark")
	once := fs.String("once", "", "run one real prompt, print the final frame, and exit (for capture)")
	width := fs.Int("width", 96, "width for -render/-once")
	height := fs.Int("height", 28, "height for -render/-once")
	workdir := fs.String("C", ".", "workspace root")
	model := fs.String("model", defaultModel, "model")
	provName := fs.String("provider", "anthropic", "provider")
	baseURL := fs.String("base-url", "", "provider base URL")
	apiKey := fs.String("api-key", "", "provider API key")
	fs.Parse(args)

	if *render {
		if *welcome {
			fmt.Print(tui.DemoWelcomeView(*width, *height, *logo))
		} else {
			fmt.Print(tui.DemoView(*width, *height))
		}
		return
	}

	prov, err := provider.New(*provName, provider.Config{BaseURL: *baseURL, APIKey: *apiKey})
	if err != nil {
		log.Fatalf("provider: %v", err)
	}
	run := func(ctx context.Context, prompt string, emit func(agent.Event)) (*agent.Result, error) {
		ag, err := agent.New(agent.Config{Name: "holocron", Model: *model, Workdir: *workdir},
			prov, agent.WithEventHook(emit))
		if err != nil {
			return nil, err
		}
		return ag.Run(ctx, prompt)
	}

	if *once != "" {
		fmt.Print(tui.RenderRun(context.Background(), *width, *height, "holocron", *model, *provName, run, *once))
		return
	}

	p := tea.NewProgram(tui.New("holocron", *model, *provName, run), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatalf("tui: %v", err)
	}
}

// briefCmd prints a saved handoff brief, or lists sessions when given no name.
func briefCmd(args []string) {
	store := storage.NewSessionStore(storePath())
	if len(args) == 0 {
		names, err := session.List(store)
		if err != nil {
			log.Fatalf("list: %v", err)
		}
		if len(names) == 0 {
			fmt.Println("no saved sessions")
			return
		}
		for _, n := range names {
			fmt.Println(n)
		}
		return
	}
	b, ok, err := session.Load(store, args[0])
	if err != nil {
		log.Fatalf("load: %v", err)
	}
	if !ok {
		log.Fatalf("no brief for session %q", args[0])
	}
	fmt.Printf("# session %s (updated %s)\n\n%s\n", b.Name, b.Updated.Format("2006-01-02 15:04"), b.Content)
}

// benchCmd runs the comparison benchmark: the same coding tasks through
// holocron and (if installed) Claude Code and Codex, reporting time, success,
// and — for holocron — cost.
func benchCmd(args []string) {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	provName := fs.String("provider", "openai", "holocron lead backend")
	baseURL := fs.String("base-url", "", "provider base URL")
	apiKey := fs.String("api-key", "", "provider API key")
	model := fs.String("model", "qwen3-coder:30b", "holocron model")
	workerModel := fs.String("worker-model", "", "holocron worker model (enables delegation)")
	external := fs.Bool("external", true, "also run Claude Code and Codex if installed")
	fs.Parse(args)

	prov, err := provider.New(*provName, provider.Config{BaseURL: *baseURL, APIKey: *apiKey})
	if err != nil {
		log.Fatalf("provider: %v", err)
	}
	runners := []bench.Runner{&bench.HolocronRunner{
		Provider: prov, Model: *model, Worker: prov, WorkerModel: *workerModel,
	}}
	if *external {
		runners = append(runners, bench.DefaultExternalRunners()...)
	}

	fmt.Printf("running %d tasks across %d runners...\n\n", len(bench.DefaultTasks()), len(runners))
	results := bench.Run(context.Background(), bench.DefaultTasks(), runners)
	fmt.Print(bench.Report(results))
}

func serveCmd(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "listen address")
	model := fs.String("model", defaultModel, "default lead model")
	workdir := fs.String("C", ".", "workspace root agents operate in")
	provName := fs.String("provider", "anthropic", "lead backend ("+strings.Join(provider.Names(), ", ")+")")
	baseURL := fs.String("base-url", "", "lead provider base URL")
	apiKey := fs.String("api-key", "", "lead provider API key")
	workerProv := fs.String("worker-provider", "openai", "worker backend")
	workerBaseURL := fs.String("worker-base-url", "", "worker provider base URL (enables delegation for sessions)")
	workerAPIKey := fs.String("worker-api-key", "", "worker provider API key")
	// SSO. Set -auth-issuer (OIDC) and/or -auth-cf-team (Cloudflare Access) and/or
	// -auth-token to enforce auth; unset = open.
	authIssuer := fs.String("auth-issuer", "", "OIDC issuer URL (Keycloak/Google/etc.)")
	authClientID := fs.String("auth-client-id", "", "OIDC client id (audience)")
	authClientSecret := fs.String("auth-client-secret", "", "OIDC client secret")
	authRedirect := fs.String("auth-redirect-url", "", "OIDC callback URL (https://host/auth/callback)")
	authEmails := fs.String("auth-allowed-emails", "", "comma-separated email allowlist (empty = any verified user)")
	authCFTeam := fs.String("auth-cf-team", "", "Cloudflare Access team domain (https://<team>.cloudflareaccess.com)")
	authCFAud := fs.String("auth-cf-aud", "", "Cloudflare Access application AUD")
	authToken := fs.String("auth-token", "", "static API bearer token for programmatic clients")
	authCookieSecret := fs.String("auth-cookie-secret", "", "hex key signing session cookies (persists sessions across restarts)")
	authInsecureCookies := fs.Bool("auth-insecure-cookies", false, "drop Secure flag on cookies (local http dev only)")
	artifactDir := fs.String("artifact-dir", "", "directory for published artifacts (default: <config>/holocron/artifacts)")
	publicURL := fs.String("public-url", "", "external origin for artifact links (e.g. https://holocron.example.com)")
	fs.Parse(args)

	lead, err := provider.New(*provName, provider.Config{BaseURL: *baseURL, APIKey: *apiKey})
	if err != nil {
		log.Fatalf("provider: %v", err)
	}
	var worker provider.Provider
	if *workerBaseURL != "" || *workerAPIKey != "" {
		worker, err = provider.New(*workerProv, provider.Config{BaseURL: *workerBaseURL, APIKey: *workerAPIKey})
		if err != nil {
			log.Fatalf("worker provider: %v", err)
		}
	}

	var cookieSecret []byte
	if *authCookieSecret != "" {
		if b, err := hex.DecodeString(*authCookieSecret); err == nil {
			cookieSecret = b
		} else {
			log.Fatalf("auth-cookie-secret must be hex: %v", err)
		}
	}
	var emails []string
	if *authEmails != "" {
		emails = strings.Split(*authEmails, ",")
	}
	authr, err := auth.New(context.Background(), auth.Config{
		Issuer:               *authIssuer,
		ClientID:             *authClientID,
		ClientSecret:         *authClientSecret,
		RedirectURL:          *authRedirect,
		CFTeamDomain:         *authCFTeam,
		CFAudience:           *authCFAud,
		StaticToken:          *authToken,
		AllowedEmails:        emails,
		CookieSecret:         cookieSecret,
		AllowInsecureCookies: *authInsecureCookies,
	})
	if err != nil {
		log.Fatalf("auth: %v", err)
	}

	artDir := *artifactDir
	if artDir == "" {
		artDir = storePath() + "/../artifacts"
	}
	arts, err := artifact.NewStore(artDir, strings.TrimRight(*publicURL, "/"))
	if err != nil {
		log.Fatalf("artifact store: %v", err)
	}

	store := storage.NewSessionStore(storePath())
	mgr := server.NewManager(lead, worker, *model, *workdir)
	// The publish tool shares a workspace file via the artifact store.
	mgr.SetPublish(func(name string, data []byte, mime string) (string, error) {
		m, err := arts.Put(data, mime, name)
		if err != nil {
			return "", err
		}
		return arts.URL(m.ID), nil
	})
	srv := server.New(store, mgr, authr, arts)

	authState := "OPEN (no auth)"
	if srv.AuthEnabled() {
		authState = "SSO enforced"
	}
	log.Printf("holocron serve on %s (provider %s, model %s, workspace %s) — %s", *addr, *provName, *model, *workdir, authState)
	if !srv.AuthEnabled() {
		log.Printf("WARNING: no auth configured; do not expose this beyond LAN/localhost")
	}
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func agentCmd(args []string) {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	workdir := fs.String("C", ".", "workspace root")
	model := fs.String("model", defaultModel, "model")
	name := fs.String("name", "agent", "agent name")
	identity := fs.String("identity", "", "agent identity / system prompt addendum")
	style := fs.String("style", "", "output verbosity: normal | terse | caveman (terser = fewer tokens)")
	provName := fs.String("provider", "anthropic", "lead model backend ("+strings.Join(provider.Names(), ", ")+")")
	baseURL := fs.String("base-url", "", "lead provider base URL (e.g. https://ollama.farmerjohnparmesan.com/v1)")
	apiKey := fs.String("api-key", "", "lead provider API key")
	// Optional worker (cost-saving delegation). When -worker-model is set the
	// lead can delegate bounded subtasks to this cheaper/local backend.
	workerProv := fs.String("worker-provider", "openai", "worker model backend")
	workerModel := fs.String("worker-model", "", "worker model id (enables delegation, e.g. qwen3-coder:30b)")
	workerBaseURL := fs.String("worker-base-url", "", "worker provider base URL")
	workerAPIKey := fs.String("worker-api-key", "", "worker provider API key")
	sessionName := fs.String("session", "", "named session: resume from its saved brief and checkpoint at the end")
	fresh := fs.Bool("fresh", false, "with -session, ignore the existing brief (still checkpoints)")
	fs.Parse(args)

	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		log.Fatal("agent: a prompt is required")
	}

	prov, err := provider.New(*provName, provider.Config{BaseURL: *baseURL, APIKey: *apiKey})
	if err != nil {
		log.Fatalf("provider: %v", err)
	}

	opts := []agent.Option{agent.WithOutput(os.Stdout)}
	if *workerModel != "" {
		wp, err := provider.New(*workerProv, provider.Config{BaseURL: *workerBaseURL, APIKey: *workerAPIKey})
		if err != nil {
			log.Fatalf("worker provider: %v", err)
		}
		opts = append(opts, agent.WithWorker(wp, *workerModel))
	}

	if *sessionName != "" {
		store := storage.NewSessionStore(storePath())
		if !*fresh {
			if b, ok, err := session.Load(store, *sessionName); err != nil {
				log.Fatalf("load session: %v", err)
			} else if ok {
				fmt.Printf("resuming session %q from brief (updated %s)\n", *sessionName, b.Updated.Format("2006-01-02 15:04"))
				opts = append(opts, agent.WithResume(b.Content))
			}
		}
		name := *sessionName
		opts = append(opts, agent.WithCheckpoint(func(brief string) error {
			return session.Save(store, name, brief)
		}))
	}

	ag, err := agent.New(agent.Config{
		Name:     *name,
		Identity: *identity,
		Model:    *model,
		Workdir:  *workdir,
		Style:    agent.Style(*style),
	}, prov, opts...)
	if err != nil {
		log.Fatalf("agent: %v", err)
	}

	res, err := ag.Run(context.Background(), prompt)
	if err != nil {
		log.Fatalf("run: %v", err)
	}

	fmt.Printf("\n--- done in %d turns ---\n", res.Turns)
	for model, u := range res.ByModel {
		fmt.Printf("  %-24s in=%d out=%d cache_read=%d  $%.4f\n",
			model, u.InputTokens, u.OutputTokens, u.CacheReadTokens, agent.Cost(model, u))
	}
	fmt.Printf("total cost: $%.4f\n", res.Cost())
}
