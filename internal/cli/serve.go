package cli

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/holocron/internal/artifact"
	"github.com/dougkirkley/holocron/internal/auth"
	"github.com/dougkirkley/holocron/internal/provider"
	"github.com/dougkirkley/holocron/internal/server"
	"github.com/dougkirkley/holocron/internal/storage"
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
			lead, err := provider.New(provName, provider.Config{BaseURL: baseURL, APIKey: apiKey})
			if err != nil {
				return fmt.Errorf("provider: %w", err)
			}
			var worker provider.Provider
			if workerBaseURL != "" || workerKey != "" {
				worker, err = provider.New(workerProv, provider.Config{BaseURL: workerBaseURL, APIKey: workerKey})
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
			srv := server.New(storage.NewSessionStore(sp), mgr, authr, arts)

			state := "OPEN (no auth)"
			if srv.AuthEnabled() {
				state = "SSO enforced"
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "holocron serve on %s (provider %s, model %s, workspace %s) — %s\n", addr, provName, model, workdir, state)
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
