package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMCPServersAllDiscoversProjectDir(t *testing.T) {
	dir := t.TempDir()
	mcpDir := filepath.Join(dir, ".dross", "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// one object (name defaults from filename), and an array file
	os.WriteFile(filepath.Join(mcpDir, "exa.json"),
		[]byte(`{"transport":"http","url":"https://mcp.exa.ai/mcp"}`), 0o644)
	os.WriteFile(filepath.Join(mcpDir, "more.json"),
		[]byte(`[{"name":"ctx","url":"https://mcp.context7.com/mcp"},{"name":"local","transport":"stdio","command":"npx","args":["-y","srv"]}]`), 0o644)

	got := map[string]config0MCP{}
	for _, s := range MCPServersAll(dir) {
		got[s.Name] = config0MCP{s.Transport, s.URL, s.Command}
	}
	if got["exa"].url != "https://mcp.exa.ai/mcp" {
		t.Errorf("exa not discovered (name from filename): %+v", got)
	}
	if got["ctx"].url == "" {
		t.Errorf("ctx not discovered from array file: %+v", got)
	}
	if got["local"].command != "npx" {
		t.Errorf("stdio server not discovered: %+v", got)
	}
}

type config0MCP struct{ transport, url, command string }
