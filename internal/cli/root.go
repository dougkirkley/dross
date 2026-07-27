// Package cli is holocron's command-line interface, built with Cobra. Each
// subcommand lives in its own file and is constructed by a newXCmd() function,
// so commands can be built and exercised in tests without a process.
package cli

import "github.com/spf13/cobra"

const defaultModel = "claude-opus-5"

// NewRootCmd builds the root command with all subcommands attached.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "holocron",
		Short:         "Cost-aware multi-model coding agent",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("config", "", "path to JSON config file (default: ./holocron.json or user config dir)")
	root.AddCommand(
		newAgentCmd(),
		newServeCmd(),
		newTUICmd(),
		newBriefCmd(),
		newBenchCmd(),
	)
	return root
}

// Execute runs the CLI.
func Execute() error {
	return NewRootCmd().Execute()
}
