package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dougkirkley/dross/internal/config"
	"github.com/dougkirkley/dross/pkg/agent"
	"github.com/dougkirkley/dross/pkg/provider"
)

// configToolDef describes the built-in `config` tool: it lets the model read or
// edit dross's own settings when the user asks (e.g. "set my worker to X").
func configToolDef() provider.ToolDef {
	return provider.ToolDef{
		Name: "config",
		Description: "Read or edit dross's own JSON config (provider, model, worker, reviewer, review, tools, permission, welcome, identity_prompt, name, phrases). " +
			"op=get returns the current config; op=set merges the `patch` object into it (only provided top-level keys are overwritten) and saves. Use this when the user asks to change dross's settings.",
		Properties: map[string]any{
			"op":    map[string]any{"type": "string", "enum": []string{"get", "set", "schema"}, "description": "get current config, set, or schema (list keys)"},
			"patch": map[string]any{"type": "object", "description": "op=set: a JSON object of top-level config keys to merge (preferred, e.g. {\"name\":\"x\"})"},
			"key":   map[string]any{"type": "string", "description": "op=set: single key to set (alternative to patch, with value)"},
			"value": map[string]any{"description": "op=set: value for key"},
		},
		Required: []string{"op"},
	}
}

// configToolHandler edits the config file at cfgPath (or the default location).
// It merges at the raw-JSON level so the custom `permission` shape and any
// unknown keys are preserved verbatim.
func configToolHandler(cfgPath string) agent.ToolHandler {
	return func(_ context.Context, input json.RawMessage) (string, bool) {
		var in struct {
			Op    string          `json:"op"`
			Patch json.RawMessage `json:"patch"`
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return err.Error(), true
		}
		path := configFilePath(cfgPath)

		if in.Op == "schema" {
			return configSchemaHelp(), false
		}
		if in.Op == "set" {
			patch, err := decodeSetArgs(in.Patch, in.Key, in.Value)
			if err != nil {
				return err.Error(), true
			}
			cur := map[string]any{}
			if b, err := os.ReadFile(path); err == nil {
				_ = json.Unmarshal(b, &cur)
			}
			for k, v := range patch {
				cur[k] = v
			}
			out, err := json.MarshalIndent(cur, "", "  ")
			if err != nil {
				return err.Error(), true
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err.Error(), true
			}
			if err := os.WriteFile(path, out, 0o600); err != nil {
				return err.Error(), true
			}
			return "config updated (" + path + "); some changes take effect on the next run or restart:\n" + string(out), false
		}

		if b, err := os.ReadFile(path); err == nil {
			return "config (" + path + "):\n" + string(b), false
		}
		return "no config file yet (" + path + "); defaults are in effect", false
	}
}

// decodeSetArgs builds the set patch from either a `patch` object or a
// `key`+`value` pair (both forms the model tends to use).
func decodeSetArgs(rawPatch json.RawMessage, key string, value json.RawMessage) (map[string]any, error) {
	if len(rawPatch) > 0 {
		p, err := decodePatch(rawPatch)
		if err != nil {
			return nil, fmt.Errorf("invalid patch: %w", err)
		}
		if len(p) > 0 {
			return p, nil
		}
	}
	if key != "" {
		var v any
		if len(value) == 0 {
			return nil, fmt.Errorf("op=set with key requires a value")
		}
		if err := json.Unmarshal(value, &v); err != nil {
			v = string(value) // treat a bare/unquoted value as a string
		}
		return map[string]any{key: v}, nil
	}
	return nil, fmt.Errorf("op=set requires a patch object, or key+value")
}

// decodePatch parses a config patch, tolerating a patch that was passed as a
// JSON string (double-encoded) instead of an object.
func decodePatch(raw json.RawMessage) (map[string]any, error) {
	var patch map[string]any
	if err := json.Unmarshal(raw, &patch); err == nil {
		return patch, nil
	}
	// Maybe it's a JSON string containing JSON.
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(s), &patch); err != nil {
		return nil, err
	}
	return patch, nil
}

// configSchemaHelp lists the editable config keys for op=schema.
func configSchemaHelp() string {
	return "dross config keys (op=set with a patch object):\n" +
		"  name, identity_prompt, welcome (clean|boxed|centered), theme (crystal|ember|matrix|grape|mono|tokyonight|catppuccin|gruvbox|dracula|nord|onedark|rose-pine|solarized),\n" +
		"  colors {role:hex}, gradient [hex...], provider, model, base_url, api_key, style,\n" +
		"  max_tokens, max_parallel, models [ids], worker {provider,model,base_url,api_key},\n" +
		"  reviewer {...}, review (bool), compactor {...}, compact_at, phrases [strings],\n" +
		"  tools [ids], permission {tool:allow|ask|deny}, plugins [...]"
}

// setConfigKey merges a single top-level key into the config file at path,
// preserving the custom `permission` shape and any unknown keys.
func setConfigKey(path, key string, val any) error {
	cur := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &cur)
	}
	cur[key] = val
	out, err := json.MarshalIndent(cur, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// configFilePath resolves where to read/write config: the first existing search
// path, else the default user config location.
func configFilePath(explicit string) string {
	if _, path, err := config.Load(explicit); err == nil && path != "" {
		return path
	}
	if explicit != "" {
		return explicit
	}
	return defaultConfigPath()
}

// newSessionID returns a short random hex id for a TUI session (used by the
// --resume hint). Falls back to a fixed label if randomness is unavailable.
func newSessionID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "session"
	}
	return hex.EncodeToString(b)
}

func defaultConfigPath() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "dross", "config.json")
	}
	return "dross.json"
}

// fmtMCP formats one MCP server for the /mcp list output.
func fmtMCP(s config.MCPServer) string {
	t := s.Transport
	if t == "" {
		t = "http"
	}
	loc := s.URL
	if t == "stdio" {
		loc = strings.TrimSpace(s.Command + " " + strings.Join(s.Args, " "))
	}
	return fmt.Sprintf("%s  (%s)  %s", s.Name, t, loc)
}

// mcpCommand backs the TUI /mcp slash command: list, add, or remove MCP servers,
// persisting to the config file and bumping gen so the agent rebuilds (and
// reconnects) on the next prompt. Deliberately TUI/config-only — MCP servers are
// never addable from the mobile app.
func mcpCommand(file *config.File, cfgPath string, gen *int, discover func() []config.MCPServer) func(args []string) []string {
	save := func() []string {
		if err := setConfigKey(cfgPath, "mcp", file.MCP); err != nil {
			return []string{"save failed: " + err.Error()}
		}
		(*gen)++
		return nil
	}
	return func(args []string) []string {
		if len(args) == 0 || args[0] == "list" {
			var out []string
			if len(file.MCP) == 0 {
				out = append(out, "no configured MCP servers · add one: /mcp add <name> <url>")
			} else {
				out = append(out, "configured (editable):")
				for _, s := range file.MCP {
					out = append(out, "  "+fmtMCP(s))
				}
			}
			if discover != nil {
				names := map[string]bool{}
				for _, s := range file.MCP {
					names[s.Name] = true
				}
				var disc []string
				for _, s := range discover() {
					if !names[s.Name] {
						disc = append(disc, "  "+fmtMCP(s))
					}
				}
				if len(disc) > 0 {
					out = append(out, "discovered (mcp/ dirs, read-only):")
					out = append(out, disc...)
				}
			}
			return out
		}
		switch args[0] {
		case "add":
			if len(args) < 3 {
				return []string{"usage: /mcp add <name> <url>   |   /mcp add <name> stdio <command> [args...]"}
			}
			name := args[1]
			var spec config.MCPServer
			if args[2] == "stdio" {
				if len(args) < 4 {
					return []string{"usage: /mcp add <name> stdio <command> [args...]"}
				}
				spec = config.MCPServer{Name: name, Transport: "stdio", Command: args[3], Args: args[4:]}
			} else {
				spec = config.MCPServer{Name: name, Transport: "http", URL: args[2]}
			}
			kept := make([]config.MCPServer, 0, len(file.MCP)+1)
			for _, s := range file.MCP {
				if s.Name != name {
					kept = append(kept, s)
				}
			}
			file.MCP = append(kept, spec)
			if msg := save(); msg != nil {
				return msg
			}
			return []string{fmt.Sprintf("added MCP server %q — connects on your next message", name)}
		case "remove", "rm":
			if len(args) < 2 {
				return []string{"usage: /mcp remove <name>"}
			}
			name := args[1]
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
				return []string{"no MCP server named " + name}
			}
			file.MCP = kept
			if msg := save(); msg != nil {
				return msg
			}
			return []string{"removed MCP server " + name}
		default:
			return []string{"usage: /mcp [list] | /mcp add <name> <url> | /mcp add <name> stdio <cmd> [args...] | /mcp remove <name>"}
		}
	}
}
