package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/dross/internal/config"
	"github.com/dougkirkley/dross/internal/plugins"
)

// newMCPCmd is `dross mcp`: manage MCP servers in the config file, like
// `claude mcp`. Edits the same config the TUI /mcp command and directory
// discovery use. Deliberately CLI/config-only — never exposed to the mobile app.
func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP servers (list, add, remove)",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List configured and discovered MCP servers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			file := loadMCPFile(cfgPath)
			out := cmd.OutOrStdout()
			if len(file.MCP) == 0 {
				fmt.Fprintln(out, "no configured MCP servers · add: dross mcp add <name> <url>")
			} else {
				fmt.Fprintln(out, "configured (editable):")
				for _, s := range file.MCP {
					fmt.Fprintln(out, "  "+fmtMCP(s))
				}
			}
			names := map[string]bool{}
			for _, s := range file.MCP {
				names[s.Name] = true
			}
			var disc []config.MCPServer
			for _, s := range plugins.MCPServersAll(".") {
				if !names[s.Name] {
					disc = append(disc, s)
				}
			}
			if len(disc) > 0 {
				fmt.Fprintln(out, "discovered (mcp/ dirs, read-only):")
				for _, s := range disc {
					fmt.Fprintln(out, "  "+fmtMCP(s))
				}
			}
			return nil
		},
	})

	var transport string
	var headers, envs []string
	add := &cobra.Command{
		Use:   "add <name> <url|command> [args...]",
		Short: "Add an MCP server (http by default; --transport stdio for a local command)",
		Long: "Add an MCP server to the config.\n\n" +
			"  http (default):  dross mcp add exa https://mcp.exa.ai/mcp\n" +
			"  stdio:           dross mcp add local --transport stdio -- npx -y some-mcp-server\n",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			name := args[0]
			spec := config.MCPServer{Name: name, Headers: kvMap(headers), Env: kvMap(envs)}
			if transport == "stdio" {
				spec.Transport = "stdio"
				spec.Command = args[1]
				spec.Args = args[2:]
			} else {
				spec.Transport = "http"
				spec.URL = args[1]
			}
			path := configFilePath(cfgPath)
			file := loadMCPFile(cfgPath)
			kept := make([]config.MCPServer, 0, len(file.MCP)+1)
			for _, s := range file.MCP {
				if s.Name != name {
					kept = append(kept, s)
				}
			}
			file.MCP = append(kept, spec)
			if err := setConfigKey(path, "mcp", file.MCP); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added MCP server %q (%s) to %s\n", name, spec.Transport, path)
			return nil
		},
	}
	add.Flags().StringVar(&transport, "transport", "http", "transport: http or stdio")
	add.Flags().StringArrayVar(&headers, "header", nil, "http header k=v (repeatable, e.g. Authorization=Bearer XYZ)")
	add.Flags().StringArrayVar(&envs, "env", nil, "stdio env var k=v (repeatable)")
	cmd.AddCommand(add)

	cmd.AddCommand(&cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a configured MCP server",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			name := args[0]
			path := configFilePath(cfgPath)
			file := loadMCPFile(cfgPath)
			kept := make([]config.MCPServer, 0, len(file.MCP))
			found := false
			for _, s := range file.MCP {
				if s.Name == name {
					found = true
					continue
				}
				kept = append(kept, s)
			}
			if !found {
				return fmt.Errorf("no MCP server named %q", name)
			}
			file.MCP = kept
			if err := setConfigKey(path, "mcp", file.MCP); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed MCP server %q\n", name)
			return nil
		},
	})

	return cmd
}

// loadMCPFile loads the config file (empty File if none exists yet).
func loadMCPFile(cfgPath string) *config.File {
	file, _, err := config.Load(cfgPath)
	if err != nil || file == nil {
		return &config.File{}
	}
	return file
}

// kvMap parses repeated "k=v" flag values into a map (nil when empty).
func kvMap(pairs []string) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		if k, v, ok := strings.Cut(p, "="); ok {
			m[k] = v
		}
	}
	return m
}
