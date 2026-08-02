// Package cli is dross's command-line interface, built with Cobra. Each
// subcommand lives in its own file and is constructed by a newXCmd() function,
// so commands can be built and exercised in tests without a process.
package cli

import "github.com/spf13/cobra"

const defaultModel = "claude-opus-5"

// Version is the dross version shown in the TUI and `dross --version`.
const Version = "v0.4.0"

// NewRootCmd builds the root command. Running dross with no subcommand launches
// the interactive TUI (the TUI's RunE + flags are attached to the root, so there
// is no separate `tui` subcommand).
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "dross",
		Short: "Cost-aware multi-model coding agent",
		Long: "Cost-aware multi-model coding agent.\n\n" +
			"Every flag can also be set from the environment, as DROSS_ plus the\n" +
			"flag in upper case with dashes as underscores: --server reads\n" +
			"DROSS_SERVER, --auth-client-id reads DROSS_AUTH_CLIENT_ID. A flag\n" +
			"given on the command line overrides the environment.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
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
	)
	return root
}

// Execute runs the CLI.
func Execute() error { return NewRootCmd().Execute() }
