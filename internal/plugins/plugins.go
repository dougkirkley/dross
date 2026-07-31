// Package plugins loads Claude-Code-compatible skills, markdown slash commands,
// and plugins from disk, and installs plugins from git-hosted marketplaces.
//
// It reads the same on-disk formats as Claude Code so existing skills and
// plugins work unchanged: SKILL.md files with YAML frontmatter, plugin.json
// manifests, and marketplace.json indexes. Discovery scans both dross-native
// (.dross/, ~/.dross/) and Claude (.claude/, ~/.claude/) locations.
package plugins

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	yaml "go.yaml.in/yaml/v4"
)

// maxFileSize caps how much of a skill/manifest file is read, bounding memory
// against a hostile plugin shipping a huge file.
const maxFileSize = 1 << 20 // 1 MiB

// readCapped reads at most maxFileSize bytes of path.
func readCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxFileSize))
}

// safeSegment rejects names that are not a single, safe path component (guards
// against path traversal when a name from untrusted marketplace/plugin data is
// joined into a filesystem path).
func safeSegment(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid name %q", name)
	}
	return nil
}

// Skill is a model- or user-invocable instruction set loaded from a SKILL.md
// file (or a flat command .md). The name+description are always cheap to keep in
// context; the Body (the actual instructions) is loaded on invocation.
type Skill struct {
	Name                   string   // invocation name (plugin skills are "plugin:skill")
	Description            string   // when to use it (progressive disclosure)
	Body                   string   // markdown instructions (frontmatter stripped)
	Dir                    string   // directory containing the skill (for ${SKILL_DIR})
	AllowedTools           []string // tools pre-approved while the skill runs
	DisableModelInvocation bool     // if true, only the user can invoke it (/name)
	UserInvocable          bool     // if false, hidden from the / menu (model-only)
	Source                 string   // "project" | "user" | plugin name
	Foreign                bool     // loaded from a .claude/ dir — off until enabled
}

// front is the recognized SKILL.md / command frontmatter.
type front struct {
	Name                   string     `yaml:"name"`
	Description            string     `yaml:"description"`
	WhenToUse              string     `yaml:"when_to_use"`
	AllowedTools           stringList `yaml:"allowed-tools"`
	DisableModelInvocation bool       `yaml:"disable-model-invocation"`
	UserInvocable          *bool      `yaml:"user-invocable"`
}

// stringList accepts a YAML list, or a space/comma-separated scalar, as []string.
type stringList []string

func (s *stringList) UnmarshalYAML(node *yaml.Node) error {
	var list []string
	if err := node.Decode(&list); err == nil {
		*s = list
		return nil
	}
	var scalar string
	if err := node.Decode(&scalar); err != nil {
		return err
	}
	*s = splitList(scalar)
	return nil
}

func splitList(s string) []string {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	out := f[:0]
	for _, v := range f {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// splitFrontmatter separates a leading `---`-delimited YAML block from the body.
func splitFrontmatter(content string) (fm, body string) {
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return "", content
	}
	rest := content[strings.IndexByte(content, '\n')+1:]
	// Find the closing --- on its own line.
	for _, sep := range []string{"\n---\n", "\n---\r\n"} {
		if i := strings.Index(rest, sep); i >= 0 {
			return rest[:i], rest[i+len(sep):]
		}
	}
	if strings.HasSuffix(rest, "\n---") {
		return strings.TrimSuffix(rest, "\n---"), ""
	}
	return "", content // unterminated frontmatter: treat all as body
}

// parseSkillFile reads a SKILL.md (or command .md), applying defaults. dirName is
// the fallback name; source labels its origin.
func parseSkillFile(path, dirName, source string) (Skill, error) {
	b, err := readCapped(path)
	if err != nil {
		return Skill{}, err
	}
	fmText, body := splitFrontmatter(string(b))
	var f front
	if fmText != "" {
		if err := yaml.Unmarshal([]byte(fmText), &f); err != nil {
			return Skill{}, err
		}
	}
	name := f.Name
	if name == "" {
		name = dirName
	}
	desc := f.Description
	if f.WhenToUse != "" {
		if desc != "" {
			desc += " "
		}
		desc += f.WhenToUse
	}
	userInvocable := true
	if f.UserInvocable != nil {
		userInvocable = *f.UserInvocable
	}
	return Skill{
		Name:                   name,
		Description:            desc,
		Body:                   strings.TrimSpace(body),
		Dir:                    filepath.Dir(path),
		AllowedTools:           f.AllowedTools,
		DisableModelInvocation: f.DisableModelInvocation,
		UserInvocable:          userInvocable,
		Source:                 source,
	}, nil
}

// skillsInDir scans dir for <name>/SKILL.md skill directories.
func skillsInDir(dir, source string) []Skill {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name(), "SKILL.md")
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if s, err := parseSkillFile(p, e.Name(), source); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// commandsInDir scans dir for flat <name>.md slash-command files. These are
// user-invocable (/name) and, unless frontmatter says otherwise, not
// model-invoked.
func commandsInDir(dir, source string) []Skill {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		s, err := parseSkillFile(filepath.Join(dir, e.Name()), name, source)
		if err != nil {
			continue
		}
		if !hasExplicitModelInvocation(filepath.Join(dir, e.Name())) {
			s.DisableModelInvocation = true // commands are user-invoked by default
		}
		out = append(out, s)
	}
	return out
}

// hasExplicitModelInvocation reports whether the file's frontmatter set
// disable-model-invocation explicitly (so we don't override an author's choice).
func hasExplicitModelInvocation(path string) bool {
	b, err := readCapped(path)
	if err != nil {
		return false
	}
	fm, _ := splitFrontmatter(string(b))
	return strings.Contains(fm, "disable-model-invocation")
}
