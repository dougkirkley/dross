package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/dross/pkg/provider/googleauth"
)

// newLoginCmd handles the browser sign-ins a provider needs when it takes no
// API key.
//
// There used to be a bare `dross login` here: a generic PKCE flow whose token
// went to a `claude-sub` provider, which spoke the Anthropic Messages API with
// a subscription token. It was removed. A Claude subscription is licensed for
// Anthropic's own clients, and the config it needed — a pinned first system
// block and an anthropic-beta header — existed only to make dross's requests
// look like the official client's. Use `claude-code`, which drives the real
// Claude Code CLI, or the `anthropic` provider with an API key.
func newLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login gemini",
		Short: "Authenticate a provider (`login gemini` for Vertex Gemini)",
		Long: "`dross login gemini` runs a Google browser login (loopback OAuth) and " +
			"caches the token so the vertex provider can call Gemini on Vertex AI " +
			"with no API key — set GOOGLE_CLOUD_PROJECT (and optionally " +
			"GOOGLE_CLOUD_LOCATION) and pick a model under the vertex provider.\n\n" +
			"For Claude: use the claude-code provider (your subscription, via the " +
			"installed claude CLI) or the anthropic provider with an API key.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "gemini", "google", "vertex":
				out := cmd.OutOrStdout()
				if err := googleauth.Login(cmd.Context(), out); err != nil {
					return fmt.Errorf("login failed: %w", err)
				}
				fmt.Fprintln(out, "signed in — set GOOGLE_CLOUD_PROJECT, then pick a model under the vertex provider (/model)")
				return nil
			default:
				return fmt.Errorf("unknown login target %q (only `login gemini` is supported)", args[0])
			}
		},
	}
	return cmd
}
