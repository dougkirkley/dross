package cli

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/dross/internal/artifact"
	"github.com/dougkirkley/dross/internal/auth"
	"github.com/dougkirkley/dross/internal/config"
	"github.com/dougkirkley/dross/internal/mail"
	"github.com/dougkirkley/dross/internal/plugins"
	"github.com/dougkirkley/dross/internal/server"
	"github.com/dougkirkley/dross/internal/storage"
	"github.com/dougkirkley/dross/pkg/agent"
	"github.com/dougkirkley/dross/pkg/provider"
)

func newServeCmd() *cobra.Command {
	var (
		addr, model, workdir                                           string
		provName, baseURL, apiKey                                      string
		workerProv, workerBaseURL, workerKey                           string
		authIssuer, authClientID, authSecret, authRedirect, authEmails string
		authCFTeam, authCFAud, authCookieSecret                        string
		authInsecure                                                   bool
		artifactDir, publicURL                                         string
		serviceToken, serviceIdentity                                  string
		adminEmail, resendKey, mailFrom                                string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP + SSE server (sessions, remote control)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := resolveProviders(cmd, provName, model)
			if err != nil {
				return err
			}
			provName, model = res.Lead.Provider, res.Lead.Model
			lead, err := provider.New(provName, provider.Config{BaseURL: res.Lead.BaseURL, APIKey: res.Lead.APIKey})
			if err != nil {
				return fmt.Errorf("provider: %w", err)
			}
			var worker provider.Provider
			if res.Worker.BaseURL != "" || res.Worker.APIKey != "" || (res.File.Worker != nil) {
				worker, err = provider.New(res.Worker.Provider, provider.Config{BaseURL: res.Worker.BaseURL, APIKey: res.Worker.APIKey})
				if err != nil {
					return fmt.Errorf("worker provider: %w", err)
				}
			}

			var cookieSecret []byte
			if authCookieSecret != "" {
				b, err := hex.DecodeString(authCookieSecret)
				if err != nil {
					return fmt.Errorf("auth-cookie-secret must be hex: %w", err)
				}
				cookieSecret = b
			}
			var emails []string
			if authEmails != "" {
				emails = strings.Split(authEmails, ",")
			}
			authr, err := auth.New(context.Background(), auth.Config{
				Issuer: authIssuer, ClientID: authClientID, ClientSecret: authSecret,
				RedirectURL: authRedirect, CFTeamDomain: authCFTeam, CFAudience: authCFAud,
				AllowedEmails: emails, CookieSecret: cookieSecret,
				ServiceToken: serviceToken, ServiceIdentity: serviceIdentity,
				AllowInsecureCookies: authInsecure,
			})
			if err != nil {
				return fmt.Errorf("auth: %w", err)
			}

			sp, err := mustStorePath()
			if err != nil {
				return err
			}
			artDir := artifactDir
			if artDir == "" {
				artDir = sp + "/../artifacts"
			}
			arts, err := artifact.NewStore(artDir, strings.TrimRight(publicURL, "/"))
			if err != nil {
				return fmt.Errorf("artifact store: %w", err)
			}

			mgr := server.NewManager(lead, worker, model, workdir)
			mgr.SetPublish(func(name string, data []byte, mime string) (string, error) {
				m, err := arts.Put(data, mime, name)
				if err != nil {
					return "", err
				}
				return arts.URL(m.ID), nil
			})
			if res.ReviewerEnabled {
				rp, err := provider.New(res.Reviewer.Provider, provider.Config{BaseURL: res.Reviewer.BaseURL, APIKey: res.Reviewer.APIKey})
				if err != nil {
					return fmt.Errorf("reviewer provider: %w", err)
				}
				mgr.SetReviewer(rp, res.Reviewer.Model)
			}
			if res.CompactorEnabled {
				cp, err := provider.New(res.Compactor.Provider, provider.Config{BaseURL: res.Compactor.BaseURL, APIKey: res.Compactor.APIKey})
				if err != nil {
					return fmt.Errorf("compactor provider: %w", err)
				}
				mgr.SetCompactor(cp, res.Compactor.Model, res.CompactAt)
				if p, err := compactStatsPath(); err == nil {
					mgr.SetCompactStats(newCompactRecorder(p))
				}
			}
			mgr.SetModels(res.File.Models)
			mgr.SetMCP(mergedMCP(res.File, workdir))
			mgr.SetPolicy(config.PolicyOptions(res.File))
			mgr.SetRepo(repoLabel(workdir))
			// The identity provider authenticates; dross authorizes. A first
			// sign-in registers the account as pending and the admin approves it
			// on /admin. Accounts already on disk migrate to approved, so the
			// deploy that introduces this does not lock the household out.
			users, err := server.NewUsers(filepath.Dir(sp))
			if err != nil {
				return fmt.Errorf("user registry: %w", err)
			}
			// The admin cannot be left waiting for themselves to approve them.
			users.Ensure(adminEmail, server.StatusApproved)

			// Pod mode. Off unless a session namespace is set, so a Docker or
			// local deployment keeps running sessions in this process and needs
			// to know nothing about Kubernetes. Set, dross writes a Session
			// resource per session and the operator turns each into a workload.
			if ns := podSessionNamespace(); ns != "" {
				pods, err := server.NewPodSessions(ns)
				if err != nil {
					return fmt.Errorf("pod sessions: %w", err)
				}
				if pods == nil {
					// Asked for, but we are not running in a cluster. Say so
					// rather than silently falling back to in-process sessions.
					return fmt.Errorf("pod sessions require running in a cluster (DROSS_SESSION_NAMESPACE=%q)", ns)
				}
				if authr == nil {
					// The credential a session pod authenticates with is minted
					// here; with no authenticator there is nothing to mint it.
					return fmt.Errorf("pod sessions require authentication to be configured")
				}
				// The backend the server resolved for itself, so a session pod
				// runs against the same one instead of the CLI default.
				pods.Provider = res.Lead.Provider
				pods.BaseURL = res.Lead.BaseURL
				pods.APIKey = res.Lead.APIKey
				pods.NamePrefix = envOr("DROSS_SESSION_PREFIX", "dross")
				pods.Image = envOr("DROSS_SESSION_IMAGE", "")
				pods.ServerURL = envOr("DROSS_SESSION_SERVER_URL", "")
				pods.Storage = envOr("DROSS_SESSION_STORAGE", "5Gi")
				pods.StorageClass = envOr("DROSS_SESSION_STORAGE_CLASS", "")
				pods.CPULimit = envOr("DROSS_SESSION_CPU_LIMIT", "")
				pods.MemoryLimit = envOr("DROSS_SESSION_MEMORY_LIMIT", "")
				pods.ConfigMap = envOr("DROSS_SESSION_CONFIGMAP", "")
				if pods.Image == "" || pods.ServerURL == "" {
					return fmt.Errorf("pod sessions need DROSS_SESSION_IMAGE and DROSS_SESSION_SERVER_URL")
				}
				pods.MintToken = func(owner, sessionID string, ttl time.Duration) (string, error) {
					return authr.MintDeviceToken(owner, sessionID, ttl)
				}
				mgr.SetPodSessions(pods)
				fmt.Fprintf(cmd.OutOrStdout(), "pod sessions enabled: namespace %s, image %s\n", ns, pods.Image)
			}

			sessionStore := storage.NewSessionStore(sp)
			srv := server.New(sessionStore, mgr, authr, arts)

			// Accounts: the registry decides who is let in, the notifier tells
			// them. Both are interfaces to the server, so a bigger store or a
			// different mail provider is a substitution here and nothing else.
			srv.SetAccounts(users, adminEmail, &mail.Templated{
				Send:   mail.NewResend(resendAPIKey(resendKey), mailFrom),
				AppURL: strings.TrimRight(publicURL, "/"),
				Admin:  adminEmail,
			})
			if authr != nil {
				authr.SetApproval(users)
				authr.OnSignIn(func(email, name string) {
					u, isNew := users.SignIn(email, name)
					if !isNew {
						return
					}
					fmt.Fprintf(cmd.OutOrStdout(), "new account %s (%s)\n", u.Email, u.Status)
					srv.AnnounceSignUp(u)
				})
			}

			// Sessions outlive the process: restore what was there, then mirror
			// changes to disk so a deploy doesn't discard the user's work.
			mgr.SetStore(sessionStore)
			restored, err := mgr.Restore()
			if err != nil {
				return fmt.Errorf("restore sessions: %w", err)
			}
			persistCtx, stopPersist := context.WithCancel(cmd.Context())
			defer stopPersist()
			go mgr.Persist(persistCtx, 2*time.Second)

			state := "OPEN (no auth)"
			if srv.AuthEnabled() {
				state = "SSO enforced"
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "dross serve on %s (provider %s, model %s, workspace %s) — %s\n", addr, provName, model, workdir, state)
			if restored > 0 {
				fmt.Fprintf(out, "restored %d session(s) from disk\n", restored)
			}
			if !srv.AuthEnabled() {
				fmt.Fprintln(out, "WARNING: no auth configured; do not expose beyond LAN/localhost")
			}
			// Explicit server: the zero-value one has no timeouts, so a stalled
			// client can hold a connection (and a goroutine) open indefinitely.
			// WriteTimeout stays 0 — SSE streams are long-lived by design.
			hs := &http.Server{
				Addr:              addr,
				Handler:           srv,
				ReadHeaderTimeout: 10 * time.Second,
				IdleTimeout:       120 * time.Second,
				MaxHeaderBytes:    1 << 16,
			}
			return hs.ListenAndServe()
		},
	}
	f := cmd.Flags()
	f.StringVar(&addr, "addr", ":8080", "listen address")
	f.StringVar(&model, "model", defaultModel, "default lead model")
	f.StringVarP(&workdir, "workdir", "C", ".", "workspace root agents operate in")
	f.StringVar(&provName, "provider", "anthropic", "lead backend")
	f.StringVar(&baseURL, "base-url", "", "lead provider base URL")
	f.StringVar(&apiKey, "api-key", "", "lead provider API key")
	f.StringVar(&workerProv, "worker-provider", "openai", "worker backend")
	f.StringVar(&workerBaseURL, "worker-base-url", "", "worker provider base URL (enables delegation)")
	f.StringVar(&workerKey, "worker-api-key", "", "worker provider API key")
	f.StringVar(&authIssuer, "auth-issuer", "", "OIDC issuer URL (Keycloak/Google/etc.)")
	f.StringVar(&authClientID, "auth-client-id", "", "OIDC client id (audience)")
	f.StringVar(&authSecret, "auth-client-secret", "", "OIDC client secret")
	f.StringVar(&authRedirect, "auth-redirect-url", "", "OIDC callback URL")
	f.StringVar(&authEmails, "auth-allowed-emails", "", "optional extra email allowlist; normally left empty so the identity provider (e.g. Cloudflare Access) is the only gate")
	f.StringVar(&authCFTeam, "auth-cf-team", "", "Cloudflare Access team domain")
	f.StringVar(&authCFAud, "auth-cf-aud", "", "Cloudflare Access application AUD")
	f.StringVar(&serviceToken, "service-token", "", "machine bearer token, authenticated as --service-identity and subject to the allowlist")
	f.StringVar(&serviceIdentity, "service-identity", "service@dross", "identity the service token authenticates as")
	f.StringVar(&authCookieSecret, "auth-cookie-secret", "", "hex key signing session cookies")
	f.BoolVar(&authInsecure, "auth-insecure-cookies", false, "drop Secure flag on cookies (local http dev)")
	f.StringVar(&adminEmail, "admin-email", "", "identity allowed to approve accounts at /admin; always approved itself")
	f.StringVar(&resendKey, "resend-key", "", "Resend API key; defaults to $RESENDER_API_KEY, and sends no mail when neither is set")
	f.StringVar(&mailFrom, "mail-from", "", "From address for account mail, e.g. \"Dross <dross@dross.sh>\"")
	f.StringVar(&artifactDir, "artifact-dir", "", "directory for published artifacts")
	f.StringVar(&publicURL, "public-url", "", "external origin for artifact links")
	return cmd
}

// repoLabel derives a short "owner/repo" label for the workspace from its git
// remote, falling back to the directory name when it isn't a git repo.
func repoLabel(workdir string) string {
	out, err := exec.Command("git", "-C", workdir, "config", "--get", "remote.origin.url").Output()
	if err == nil {
		url := strings.TrimSpace(string(out))
		url = strings.TrimSuffix(url, ".git")
		// Normalize scp-style (git@host:owner/repo) and URL forms to path parts.
		url = strings.ReplaceAll(url, ":", "/")
		var parts []string
		for _, p := range strings.Split(url, "/") {
			if p != "" && !strings.Contains(p, "@") { // drop scheme/user@host bits
				parts = append(parts, p)
			}
		}
		if len(parts) >= 2 {
			return parts[len(parts)-2] + "/" + parts[len(parts)-1]
		}
	}
	abs, err := filepath.Abs(workdir)
	if err != nil {
		abs = workdir
	}
	return filepath.Base(abs)
}

// mergedMCP combines configured MCP servers (which win on a name clash) with
// those discovered in mcp/ directories (project/user/plugins), deduped by name,
// and returns agent specs.
func mergedMCP(file *config.File, workdir string) []agent.MCPServerSpec {
	combined := append([]config.MCPServer{}, file.MCP...)
	combined = append(combined, plugins.MCPServersAll(workdir)...)
	seen := map[string]bool{}
	uniq := combined[:0]
	for _, s := range combined {
		if s.Name == "" || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		uniq = append(uniq, s)
	}
	return mcpSpecs(uniq)
}

// mcpSpec converts one configured MCP server to an agent spec.
func mcpSpec(s config.MCPServer) agent.MCPServerSpec {
	return agent.MCPServerSpec{
		Name:      s.Name,
		Transport: s.Transport,
		URL:       s.URL,
		Headers:   s.Headers,
		Command:   s.Command,
		Args:      s.Args,
		Env:       s.Env,
	}
}

// mcpSpecs converts configured MCP servers to agent specs.
func mcpSpecs(in []config.MCPServer) []agent.MCPServerSpec {
	out := make([]agent.MCPServerSpec, 0, len(in))
	for _, s := range in {
		out = append(out, mcpSpec(s))
	}
	return out
}

// resendAPIKey resolves the mail credential: the flag wins, otherwise
// $RESENDER_API_KEY. The environment is where a deployment puts it — read
// directly rather than expanded into an arg, so a secret key that does not
// exist yet leaves the value empty (the server then sends nothing) instead of
// handing the provider a literal "$(RESENDER_API_KEY)".
func resendAPIKey(flag string) string {
	if flag != "" {
		return flag
	}
	return os.Getenv("RESENDER_API_KEY")
}

// podSessionNamespace names the namespace session workloads go in, and is what
// switches pod mode on. Read from the environment rather than a flag because
// it is set by whatever deploys dross, not by whoever runs it.
func podSessionNamespace() string { return os.Getenv("DROSS_SESSION_NAMESPACE") }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
