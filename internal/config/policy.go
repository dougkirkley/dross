package config

import "github.com/dougkirkley/dross/pkg/agent"

// PolicyOptions turns the parts of a config that decide what an agent is
// allowed to do into agent options.
//
// These are shared rather than rebuilt per caller because they are the ones
// that must not differ between them. Everything else an agent is given is
// local to its host — the server wires publishing and compaction, the TUI
// wires checkpoints and skills — but which tools exist, and which of them need
// asking about, is the user's policy and applies wherever their agent runs.
// Keeping it in one place is what stops a second entry point from quietly
// running with none of it, which is how the relay ended up executing prompts
// with no rules at all.
// A nil config carries no policy, which is the same as an empty one.
func PolicyOptions(f *File) []agent.Option {
	if f == nil {
		return nil
	}
	var opts []agent.Option
	if len(f.Tools) > 0 {
		opts = append(opts, agent.WithAllowedTools(f.Tools))
	}
	if len(f.Plugins) > 0 {
		specs := make([]agent.PluginSpec, 0, len(f.Plugins))
		for _, p := range f.Plugins {
			specs = append(specs, agent.PluginSpec{
				Name: p.Name, Description: p.Description, Command: p.Command,
				Properties: p.Properties, Required: p.Required,
			})
		}
		opts = append(opts, agent.WithPluginTools(specs))
	}
	if len(f.Permission) > 0 {
		rules := make(map[string]agent.PermRuleSpec, len(f.Permission))
		for tool, p := range f.Permission {
			rules[tool] = agent.PermRuleSpec{Decision: p.Blanket, Patterns: p.Rules}
		}
		opts = append(opts, agent.WithPermissionRules(rules))
	}
	return opts
}
