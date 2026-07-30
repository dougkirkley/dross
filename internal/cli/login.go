package cli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/dross/internal/config"
	"github.com/dougkirkley/dross/internal/subauth"
	"github.com/dougkirkley/dross/pkg/provider/googleauth"
)

func newLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login [gemini]",
		Short: "Authenticate a provider (claude-sub via config OAuth, or `login gemini` for Vertex Gemini)",
		Long: "With no argument, runs the OAuth (PKCE) login for the [oauth] client in " +
			"your config and stores the token for the claude-sub provider.\n\n" +
			"`dross login gemini` runs a Google browser login (loopback OAuth) and " +
			"caches the token so the vertex provider can call Gemini on Vertex AI " +
			"with no API key — set GOOGLE_CLOUD_PROJECT (and optionally " +
			"GOOGLE_CLOUD_LOCATION) and pick a model under the vertex provider.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				switch args[0] {
				case "gemini", "google", "vertex":
					out := cmd.OutOrStdout()
					if err := googleauth.Login(cmd.Context(), out); err != nil {
						return fmt.Errorf("login failed: %w", err)
					}
					fmt.Fprintln(out, "signed in — set GOOGLE_CLOUD_PROJECT, then pick a model under the vertex provider (/model)")
					return nil
				default:
					return fmt.Errorf("unknown login target %q (use `login` or `login gemini`)", args[0])
				}
			}
			cfgPath, _ := cmd.Flags().GetString("config")
			f, _, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			if !f.OAuth.Configured() {
				return fmt.Errorf("no [oauth] client configured; add an oauth block (client_id, authorize_url, token_url, redirect_uri) to your config")
			}
			p, err := subauth.Begin(*f.OAuth)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "Open this URL, authorize, then paste the returned code:")
			fmt.Fprintln(out, "\n  "+p.URL+"\n")
			fmt.Fprint(out, "code: ")
			line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
			if _, err := subauth.Complete(*f.OAuth, p, strings.TrimSpace(line)); err != nil {
				return fmt.Errorf("login failed: %w", err)
			}
			fmt.Fprintln(out, "logged in — use --provider claude-sub")
			return nil
		},
	}
	return cmd
}
