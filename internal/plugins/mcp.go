package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/dougkirkley/dross/internal/config"
)

// MCPServersAll discovers MCP server definitions from mcp/ directories, mirroring
// skill/command discovery: project (.dross, .claude under workdir), then user
// (~/.dross, ~/.claude), then enabled installed plugins. Each *.json file holds
// one server object or an array of them; a file with no "name" defaults to its
// filename. Later duplicates by name are dropped (project shadows user/plugin).
func MCPServersAll(workdir string) []config.MCPServer {
	var all []config.MCPServer
	for _, root := range projectRoots(workdir) {
		all = append(all, mcpInDir(filepath.Join(root, "mcp"))...)
	}
	for _, root := range userRoots() {
		all = append(all, mcpInDir(filepath.Join(root, "mcp"))...)
	}
	if st, err := LoadState(); err == nil {
		for _, p := range st.Plugins {
			if !p.Enabled {
				continue
			}
			all = append(all, mcpInDir(filepath.Join(p.Dir, "mcp"))...)
		}
	}
	return dedupeMCP(all)
}

// mcpInDir reads every <dir>/*.json as one MCP server (or an array of them).
func mcpInDir(dir string) []config.MCPServer {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []config.MCPServer
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".json")
		var arr []config.MCPServer
		if json.Unmarshal(b, &arr) == nil && len(arr) > 0 {
			out = append(out, arr...)
			continue
		}
		var one config.MCPServer
		if json.Unmarshal(b, &one) == nil {
			if one.Name == "" {
				one.Name = base
			}
			if one.URL != "" || one.Command != "" {
				out = append(out, one)
			}
		}
	}
	return out
}

func dedupeMCP(in []config.MCPServer) []config.MCPServer {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if s.Name == "" || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		out = append(out, s)
	}
	return out
}
