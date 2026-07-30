package cli

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/dross/internal/artifact"
	"github.com/dougkirkley/dross/internal/auth"
	"github.com/dougkirkley/dross/internal/config"
	"github.com/dougkirkley/dross/pkg/agent"
	"github.com/dougkirkley/dross/pkg/provider"
	"github.com/dougkirkley/dross/internal/server"
	"github.com/dougkirkley/dross/internal/storage"
)

func newServeCmd() *cobra.Command {
	var (
		addr, model, workdir                                           string
		provName, baseURL, apiKey                                      string
		workerProv, workerBaseURL, workerKey                           string
		authIssuer, authClientID, authSecret, authRedirect, authEmails string
		authCFTeam, authCFAud, authToken, authCookieSecret             string
		authInsecure                                                   bool
		artifactDir, publicURL                                         string
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
				StaticToken: authToken, AllowedEmails: emails, CookieSecret: cookieSecret,
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
			mgr.SetMCP(mcpSpecs(res.File.MCP))
			mgr.SetRepo(repoLabel(workdir))
			srv := server.New(storage.NewSessionStore(sp), mgr, authr, arts)

			state := "OPEN (no auth)"
			if srv.AuthEnabled() {
				state = "SSO enforced"
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "dross serve on %s (provider %s, model %s, workspace %s) — %s\n", addr, provName, model, workdir, state)
			if !srv.AuthEnabled() {
				fmt.Fprintln(out, "WARNING: no auth configured; do not expose beyond LAN/localhost")
			}
			return http.ListenAndServe(addr, srv)
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
	f.StringVar(&authEmails, "auth-allowed-emails", "", "comma-separated email allowlist")
	f.StringVar(&authCFTeam, "auth-cf-team", "", "Cloudflare Access team domain")
	f.StringVar(&authCFAud, "auth-cf-aud", "", "Cloudflare Access application AUD")
	f.StringVar(&authToken, "auth-token", "", "static API bearer token")
	f.StringVar(&authCookieSecret, "auth-cookie-secret", "", "hex key signing session cookies")
	f.BoolVar(&authInsecure, "auth-insecure-cookies", false, "drop Secure flag on cookies (local http dev)")
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

// mcpSpecs converts configured MCP servers to agent specs.
func mcpSpecs(in []config.MCPServer) []agent.MCPServerSpec {
	out := make([]agent.MCPServerSpec, 0, len(in))
	for _, s := range in {
		out = append(out, agent.MCPServerSpec{
			Name:      s.Name,
			Transport: s.Transport,
			URL:       s.URL,
			Headers:   s.Headers,
			Command:   s.Command,
			Args:      s.Args,
			Env:       s.Env,
		})
	}
	return out
}
