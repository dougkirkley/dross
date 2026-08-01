package cli

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/dross/pkg/provider"
	"github.com/dougkirkley/dross/internal/relay"
)

// newRCCmd is `dross rc`: link this computer to a dross server as a
// device, so the phone app can drive sessions that run here. The laptop dials
// OUT over a WebSocket (the phone never needs to reach it directly); the server
// relays prompts and streams the agent's events back. The link authenticates as
// the SSO user, so a device session is always tied to the logged-in account.
func newRCCmd() *cobra.Command {
	var (
		server, token, name, workdir      string
		provName, model, baseURL, apiKey  string
		workerProv, workerModel, workerBU string
	)
	cmd := &cobra.Command{
		Use:   "rc",
		Short: "Link this computer to a dross server so the phone can drive it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := resolveProviders(cmd, provName, model)
			if err != nil {
				return err
			}
			lead, err := provider.New(res.Lead.Provider, provider.Config{BaseURL: res.Lead.BaseURL, APIKey: res.Lead.APIKey})
			if err != nil {
				return fmt.Errorf("provider: %w", err)
			}
			var worker provider.Provider
			if res.WorkerEnabled {
				worker, _ = provider.New(res.Worker.Provider, provider.Config{BaseURL: res.Worker.BaseURL, APIKey: res.Worker.APIKey})
			}
			if name == "" {
				name, _ = os.Hostname()
			}
			if name == "" {
				name = "device"
			}
			abs, _ := filepath.Abs(workdir)
			out := cmd.OutOrStdout()
			ctx := cmd.Context()

			// Resolve the server URL: flag, else $DROSS_SERVER. Without it the
			// login URL is malformed and no browser opens.
			if server == "" {
				server = os.Getenv("DROSS_SERVER")
			}
			if server == "" {
				return fmt.Errorf("no server set: pass --server https://your-dross-host (or set DROSS_SERVER)")
			}
			if u, perr := url.Parse(server); perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("invalid server address %q: use a full URL like https://your-dross-host", server)
			}

			// Authenticate as the SSO user (stored session, or sign in now).
			// $DROSS_TOKEN comes before the stored session because it is how a
			// session pod is given its scoped credential: it has no stored
			// session, no browser, and no way to complete a sign-in.
			if token == "" {
				token = os.Getenv("DROSS_TOKEN")
			}
			if token == "" {
				token = relay.LoadToken(server)
			}
			if token == "" {
				fmt.Fprintln(out, "no session — opening browser to sign in…")
				token, err = relay.Login(ctx, server)
				if err != nil {
					return fmt.Errorf("login: %w", err)
				}
			}

			return relay.RunDevice(ctx, relay.DeviceOptions{
				Server: server, Token: token, Name: name, Workdir: abs, Repo: repoLabel(workdir),
				Lead: lead, LeadModel: res.Lead.Model, Worker: worker, WorkerModel: res.Worker.Model,
				Log: func(s string) { fmt.Fprintln(out, s) },
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&server, "server", "", "dross server URL (e.g. https://dross.example.com)")
	f.StringVar(&token, "token", "", "session token (default: SSO login)")
	f.StringVar(&name, "name", "", "device name (default: hostname)")
	f.StringVarP(&workdir, "workdir", "C", ".", "workspace root sessions run in")
	f.StringVar(&provName, "provider", "anthropic", "lead backend")
	f.StringVar(&model, "model", defaultModel, "lead model")
	f.StringVar(&baseURL, "base-url", "", "lead provider base URL")
	f.StringVar(&apiKey, "api-key", "", "lead provider API key")
	f.StringVar(&workerProv, "worker-provider", "openai", "worker backend")
	f.StringVar(&workerModel, "worker-model", "", "worker model id (enables delegation)")
	f.StringVar(&workerBU, "worker-base-url", "", "worker provider base URL")
	return cmd
}
