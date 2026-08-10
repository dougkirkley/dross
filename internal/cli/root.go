// Package cli is dross's command-line interface, built with Cobra. Each
// subcommand lives in its own file and is constructed by a newXCmd() function,
// so commands can be built and exercised in tests without a process.
package cli

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

const defaultModel = "claude-opus-5"

// NewRootCmd builds the root command. Running dross with no subcommand launches
// the interactive TUI (the TUI's RunE + flags are attached to the root, so there
// is no separate `tui` subcommand).
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "dross [prompt]",
		Short: "Cost-aware multi-model coding agent",
		Long: "Cost-aware multi-model coding agent.\n\n" +
			"Every flag can also be set from the environment, as DROSS_ plus the\n" +
			"flag in upper case with dashes as underscores: --server reads\n" +
			"DROSS_SERVER, --auth-client-id reads DROSS_AUTH_CLIENT_ID. A flag\n" +
			"given on the command line overrides the environment.",
		// Wires the constant above to cobra, which is what makes `dross --version`
		// and `dross version` work. The constant existed and was shown in the
		// TUI, but the flag every CLI has did not exist -- the first thing
		// anyone runs after installing answered "unknown flag: --version".
		Version:       FullVersion(),
		SilenceUsage:  true,
		SilenceErrors: true,
		// A bare word that names no subcommand is a prompt, not a mistake —
		// `dross "fix the build"` has to work, and that is also how the Claude
		// Code CLI reads its argument. Cobra dispatches real subcommands before
		// this runs, so only the leftovers arrive here.
		Args: rootArgs,
		// Runs for the root and for every subcommand, against whichever command
		// is actually executing, so its own flags and any inherited ones are
		// covered.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return bindEnv(cmd)
		},
	}
	root.PersistentFlags().String("config", "", "path to JSON config file (default: ./dross.json or user config dir)")

	tui := newTUICmd()
	root.RunE = tui.RunE
	root.Flags().AddFlagSet(tui.Flags())

	root.AddCommand(
		newAgentCmd(),
		newServeCmd(),
		newBriefCmd(),
		newBenchCmd(),
		newLoginCmd(),
		newCompactStatsCmd(),
		newRCCmd(),
		newPluginCmd(),
		newMCPCmd(),
		newProviderCmd(),
	)
	return root
}

// rootArgs takes the positional prompt the way the Claude Code CLI takes it,
// which is to say it takes anything.
//
// A word naming no subcommand is a prompt, not a mistake — `dross "fix the
// build"` has to reach the agent — and that costs the unknown-command error,
// since cobra only raises one exactly where the prompt now goes. Extra words
// are promptArg's business rather than this validator's: `claude -p fix the
// bug` answers "fix" and drops the rest, and dross does the same, so the two
// agree on the prompts nobody meant to write as well as on the ones they did.
func rootArgs(_ *cobra.Command, _ []string) error { return nil }

// Execute runs the CLI.
//
// SIGTERM cancels the command's context rather than killing the process, which
// is what lets a session pod finish the answer it is part-way through when
// Kubernetes replaces it. Not SIGINT: the TUI reads ctrl+c as a keystroke and
// handles it itself, and a signal handler would take that away.
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	return NewRootCmd().ExecuteContext(ctx)
}
