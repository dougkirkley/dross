package cli

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/dross/internal/config"
	"github.com/dougkirkley/dross/internal/forge"
	"github.com/dougkirkley/dross/internal/relay"
	"github.com/dougkirkley/dross/pkg/provider"
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
		repo, repoBranch, cloneURL        string
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

			// Every flag reads its own DROSS_ variable, so --server is already
			// resolved by the time this runs. Without it the login URL is
			// malformed and no browser opens.
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
			// --token is filled from DROSS_TOKEN like any other flag; this only
			// works out which to name, since a session pod is handed its
			// credential that way and saying so makes a failure legible.
			source := "--token"
			if from := envSource(cmd, "token"); from != "" {
				source = from
			}
			if token == "" {
				if token = relay.LoadToken(server); token != "" {
					source = "stored session"
				}
			}
			if token != "" {
				// Which credential, never the credential itself.
				fmt.Fprintf(out, "authenticating with %s\n", source)
			}
			if token == "" {
				fmt.Fprintln(out, "no session — opening browser to sign in…")
				token, err = relay.Login(ctx, server)
				if err != nil {
					return fmt.Errorf("login: %w", err)
				}
			}

			// The repository this session starts on, cloned before the device
			// attaches so the workspace holds the code by the time a prompt can
			// reach it. The session then runs inside the clone rather than
			// beside it, which is what "a session on this repository" means and
			// saves every prompt beginning with a cd.
			label := repoLabel(workdir)
			if repo != "" && cloneURL != "" {
				// Already there on a restart: the pod comes back onto the same
				// volume after a node reboot or an image change, and cloning
				// over a checkout would throw away whatever work is in it.
				if _, statErr := os.Stat(filepath.Join(abs, ".git")); statErr == nil {
					fmt.Fprintf(out, "%s is already cloned\n", repo)
				} else {
					fmt.Fprintf(out, "cloning %s…\n", repo)
					if err := forge.Clone(ctx, abs, cloneURL, repoBranch); err != nil {
						// Fatal. A session whose point is this repository is not
						// worth attaching without it, and failing here puts the
						// reason on the pod where somebody can read it instead
						// of leaving an empty workspace to explain itself.
						return fmt.Errorf("clone %s: %w", repo, err)
					}
				}
				label = repo
			}

			opts := relay.DeviceOptions{
				Server: server, Token: token, Name: name, Workdir: abs, Repo: label,
				Lead: lead, LeadModel: res.Lead.Model, Worker: worker, WorkerModel: res.Worker.Model,
				Policy: config.PolicyOptions(res.File),
				Log:    func(s string) { fmt.Fprintln(out, s) },
			}
			err = relay.RunDevice(ctx, opts)
			// A stored session lasts twelve hours, so the common way to meet a
			// 401 here is to have left the link up overnight. Sign in again and
			// carry on, rather than making the expiry the user's problem to
			// diagnose from a WebSocket handshake code.
			//
			// Only for the stored one. A token given with --token or through
			// the environment is a deliberate choice of credential, and quietly
			// replacing it with whoever happens to be signed into the browser
			// would attach the device as the wrong identity.
			if errors.Is(err, relay.ErrUnauthorized) && source == "stored session" {
				fmt.Fprintln(out, "stored session expired — opening browser to sign in…")
				if opts.Token, err = relay.Login(ctx, server); err != nil {
					return fmt.Errorf("login: %w", err)
				}
				return relay.RunDevice(ctx, opts)
			}
			return err
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
	f.StringVar(&repo, "repo", "", "repository to start this session on (owner/name)")
	f.StringVar(&repoBranch, "repo-branch", "", "branch to check out (default: the repository's own)")
	// Deliberately not documented as something to type. It holds an access
	// token, and an argument is visible to every process on the machine; the
	// pod is given it through DROSS_CLONE_URL from a Secret, which is what this
	// flag exists to receive.
	f.StringVar(&cloneURL, "clone-url", "", "")
	_ = f.MarkHidden("clone-url")
	return cmd
}
