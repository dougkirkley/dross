package cli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/holocron/internal/config"
	"github.com/dougkirkley/holocron/internal/subauth"
)

func newLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate the claude-sub provider via your configured OAuth client",
		Long: "Runs the OAuth (PKCE) login for the [oauth] client in your config and " +
			"stores the token for the claude-sub provider. holocron ships no OAuth " +
			"client — you supply the client id, endpoints, and any beta header / " +
			"system pin in the config's [oauth] block.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
