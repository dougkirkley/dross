package plugins

import (
	"os"
	"path/filepath"
)

// LoadAll discovers every available skill and slash command, in precedence
// order: project (.ergon, .claude under workdir), then user (~/.ergon,
// ~/.claude), then installed plugins. Later duplicates of a name are dropped, so
// project skills shadow user and plugin ones.
func LoadAll(workdir string) []Skill {
	var all []Skill
	add := func(s []Skill) { all = append(all, s...) }

	// Project scope.
	for _, root := range projectRoots(workdir) {
		add(skillsInDir(filepath.Join(root, "skills"), "project"))
		add(commandsInDir(filepath.Join(root, "commands"), "project"))
	}
	// User scope.
	for _, root := range userRoots() {
		add(skillsInDir(filepath.Join(root, "skills"), "user"))
		add(commandsInDir(filepath.Join(root, "commands"), "user"))
	}
	// Installed, enabled plugins (skills namespaced "plugin:skill").
	if st, err := LoadState(); err == nil {
		for _, p := range st.Plugins {
			if !p.Enabled {
				continue
			}
			for _, s := range skillsInDir(filepath.Join(p.Dir, "skills"), p.Name) {
				s.Name = p.Name + ":" + s.Name
				all = append(all, s)
			}
			for _, s := range commandsInDir(filepath.Join(p.Dir, "commands"), p.Name) {
				s.Name = p.Name + ":" + s.Name
				all = append(all, s)
			}
		}
	}

	return dedupe(all)
}

func projectRoots(workdir string) []string {
	if workdir == "" {
		workdir = "."
	}
	return []string{filepath.Join(workdir, ".ergon"), filepath.Join(workdir, ".claude")}
}

func userRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".ergon"), filepath.Join(home, ".claude")}
}

func dedupe(in []Skill) []Skill {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		out = append(out, s)
	}
	return out
}

// ModelInvocable returns the skills the model may auto-invoke (progressive
// disclosure): those not marked disable-model-invocation.
func ModelInvocable(skills []Skill) []Skill {
	var out []Skill
	for _, s := range skills {
		if !s.DisableModelInvocation {
			out = append(out, s)
		}
	}
	return out
}

// Find returns the skill with the given name, if present.
func Find(skills []Skill, name string) (Skill, bool) {
	for _, s := range skills {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}
