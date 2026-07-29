package cli

import (
	"context"
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
			"op":    map[string]any{"type": "string", "enum": []string{"get", "set"}, "description": "get or set"},
			"patch": map[string]any{"type": "object", "description": "top-level config keys to set (op=set)"},
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

		if in.Op == "set" {
			if len(in.Patch) == 0 {
				return "op=set requires a non-empty patch object", true
			}
			cur := map[string]any{}
			if b, err := os.ReadFile(path); err == nil {
				_ = json.Unmarshal(b, &cur)
			}
			var patch map[string]any
			if err := json.Unmarshal(in.Patch, &patch); err != nil {
				return "invalid patch: " + err.Error(), true
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

func defaultConfigPath() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "ergon", "config.json")
	}
	return "ergon.json"
}
