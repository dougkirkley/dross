// Package cli is ergon's command-line interface, built with Cobra. Each
// subcommand lives in its own file and is constructed by a newXCmd() function,
// so commands can be built and exercised in tests without a process.
package cli

import "github.com/spf13/cobra"

const defaultModel = "claude-opus-5"

// Version is the ergon version shown in the TUI and `ergon --version`.
const Version = "v0.4.0"

// NewRootCmd builds the root command. Running ergon with no subcommand launches
// the interactive TUI (the TUI's RunE + flags are attached to the root, so there
// is no separate `tui` subcommand).
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "ergon",
		Short:         "Cost-aware multi-model coding agent",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}
	root.PersistentFlags().String("config", "", "path to JSON config file (default: ./ergon.json or user config dir)")

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
	)
	return root
}

// Execute runs the CLI.
func Execute() error { return NewRootCmd().Execute() }
