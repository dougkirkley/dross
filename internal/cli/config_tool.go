package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/dougkirkley/ergon/internal/config"
	"github.com/dougkirkley/ergon/pkg/agent"
	"github.com/dougkirkley/ergon/pkg/provider"
)

// configToolDef describes the built-in `config` tool: it lets the model read or
// edit ergon's own settings when the user asks (e.g. "set my worker to X").
func configToolDef() provider.ToolDef {
	return provider.ToolDef{
		Name: "config",
		Description: "Read or edit ergon's own JSON config (provider, model, worker, reviewer, review, tools, permission, welcome, identity_prompt, name, phrases). " +
			"op=get returns the current config; op=set merges the `patch` object into it (only provided top-level keys are overwritten) and saves. Use this when the user asks to change ergon's settings.",
		Properties: map[string]any{
			"op":    map[string]any{"type": "string", "enum": []string{"get", "set", "schema"}, "description": "get current config, set (with patch), or schema (list keys)"},
			"patch": map[string]any{"type": "object", "description": "top-level config keys to set (op=set); a JSON object"},
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
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return err.Error(), true
		}
		path := configFilePath(cfgPath)

		if in.Op == "schema" {
			return configSchemaHelp(), false
		}
		if in.Op == "set" {
			if len(in.Patch) == 0 {
				return "op=set requires a non-empty patch object", true
			}
			patch, err := decodePatch(in.Patch)
			if err != nil {
				return "invalid patch: " + err.Error(), true
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
	return "ergon config keys (op=set with a patch object):\n" +
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
		return filepath.Join(dir, "ergon", "config.json")
	}
	return "ergon.json"
}
