package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/dross/internal/plugins"
)

// newPluginCmd builds `dross plugin ...`: manage marketplaces and plugins,
// Claude-Code-compatible (git-hosted marketplaces, plugin.json, SKILL.md).
func newPluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage plugins and marketplaces (skills, commands)",
	}
	cmd.AddCommand(
		newMarketplaceCmd(),
		&cobra.Command{
			Use:   "install <name[@marketplace]>",
			Short: "Install a plugin from an added marketplace",
			Args:  cobra.ExactArgs(1),
			RunE: func(c *cobra.Command, args []string) error {
				p, err := plugins.Install(args[0])
				if err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "installed %s (from %s)\n", p.Name, p.Marketplace)
				return nil
			},
		},
		&cobra.Command{
			Use:   "uninstall <name>",
			Short: "Remove an installed plugin",
			Args:  cobra.ExactArgs(1),
			RunE:  func(c *cobra.Command, args []string) error { return plugins.Uninstall(args[0]) },
		},
		&cobra.Command{
			Use:   "enable <name>",
			Short: "Enable an installed plugin",
			Args:  cobra.ExactArgs(1),
			RunE:  func(c *cobra.Command, args []string) error { return plugins.SetEnabled(args[0], true) },
		},
		&cobra.Command{
			Use:   "disable <name>",
			Short: "Disable an installed plugin",
			Args:  cobra.ExactArgs(1),
			RunE:  func(c *cobra.Command, args []string) error { return plugins.SetEnabled(args[0], false) },
		},
		&cobra.Command{
			Use:   "list",
			Short: "List installed plugins",
			RunE: func(c *cobra.Command, _ []string) error {
				st, err := plugins.LoadState()
				if err != nil {
					return err
				}
				if len(st.Plugins) == 0 {
					fmt.Fprintln(c.OutOrStdout(), "no plugins installed")
					return nil
				}
				for _, p := range st.Plugins {
					state := "enabled"
					if !p.Enabled {
						state = "disabled"
					}
					fmt.Fprintf(c.OutOrStdout(), "%-24s %-10s %s\n", p.Name, state, p.Marketplace)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "skills",
			Short: "List discovered skills (project, user, plugins)",
			RunE: func(c *cobra.Command, _ []string) error {
				skills := plugins.LoadAll(".")
				if len(skills) == 0 {
					fmt.Fprintln(c.OutOrStdout(), "no skills found")
					return nil
				}
				for _, s := range skills {
					fmt.Fprintf(c.OutOrStdout(), "%-28s [%s] %s\n", s.Name, s.Source, s.Description)
				}
				return nil
			},
		},
	)
	return cmd
}

func newMarketplaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "marketplace",
		Aliases: []string{"mp"},
		Short:   "Manage plugin marketplaces (git repos)",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "add <owner/repo | git-url | path>",
			Short: "Add a marketplace and clone it",
			Args:  cobra.ExactArgs(1),
			RunE: func(c *cobra.Command, args []string) error {
				m, err := plugins.AddMarketplace(args[0])
				if err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "added marketplace %q (%d plugins)\n", m.Name, len(m.Plugins))
				for _, p := range m.Plugins {
					fmt.Fprintf(c.OutOrStdout(), "  %-24s %s\n", p.Name, p.Description)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List added marketplaces",
			RunE: func(c *cobra.Command, _ []string) error {
				st, err := plugins.LoadState()
				if err != nil {
					return err
				}
				if len(st.Marketplaces) == 0 {
					fmt.Fprintln(c.OutOrStdout(), "no marketplaces added")
					return nil
				}
				for _, m := range st.Marketplaces {
					fmt.Fprintf(c.OutOrStdout(), "%-24s %s\n", m.Name, m.Source)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "remove <name>",
			Short: "Remove an added marketplace",
			Args:  cobra.ExactArgs(1),
			RunE:  func(c *cobra.Command, args []string) error { return plugins.RemoveMarketplace(args[0]) },
		},
	)
	return cmd
}
