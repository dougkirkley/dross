package plugins

import (
	"os"
	"path/filepath"
	"testing"

	yaml "go.yaml.in/yaml/v4"
)

func TestSplitFrontmatter(t *testing.T) {
	fm, body := splitFrontmatter("---\nname: x\ndescription: hi\n---\nBody here\n")
	if fm != "name: x\ndescription: hi" {
		t.Fatalf("frontmatter = %q", fm)
	}
	if body != "Body here\n" {
		t.Fatalf("body = %q", body)
	}
	// No frontmatter.
	if fm, body := splitFrontmatter("just body"); fm != "" || body != "just body" {
		t.Fatalf("no-frontmatter = %q / %q", fm, body)
	}
}

func TestParseSkillFile(t *testing.T) {
	dir := t.TempDir()
	sd := filepath.Join(dir, "review")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\ndescription: Review a diff for bugs\nallowed-tools: read, bash\n---\nDo the review carefully.\n"
	if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := parseSkillFile(filepath.Join(sd, "SKILL.md"), "review", "project")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "review" || s.Description != "Review a diff for bugs" {
		t.Fatalf("parsed = %+v", s)
	}
	if len(s.AllowedTools) != 2 || s.AllowedTools[0] != "read" {
		t.Fatalf("allowed-tools = %v", s.AllowedTools)
	}
	if s.Body != "Do the review carefully." {
		t.Fatalf("body = %q", s.Body)
	}
	if !s.UserInvocable || s.DisableModelInvocation {
		t.Fatalf("invocation defaults wrong: %+v", s)
	}
}

func TestLoadAllProjectScope(t *testing.T) {
	dir := t.TempDir()
	sd := filepath.Join(dir, ".ergon", "skills", "greet")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte("---\ndescription: say hi\n---\nSay hi.\n"), 0o644)

	skills := LoadAll(dir)
	if _, ok := Find(skills, "greet"); !ok {
		t.Fatalf("greet not discovered: %+v", skills)
	}
	if len(ModelInvocable(skills)) == 0 {
		t.Fatal("greet should be model-invocable")
	}
}

func TestPluginSourceUnmarshal(t *testing.T) {
	// string form → relative path
	var p PluginSource
	if err := yaml.Unmarshal([]byte(`"./plugins/foo"`), &p); err != nil {
		t.Fatal(err)
	}
	if p.Path != "./plugins/foo" {
		t.Fatalf("string source = %+v", p)
	}
	// object form → github
	var g struct {
		Source PluginSource `yaml:"source"`
	}
	if err := yaml.Unmarshal([]byte("source:\n  source: github\n  repo: owner/repo\n"), &g); err != nil {
		t.Fatal(err)
	}
	if g.Source.Type != "github" || g.Source.Repo != "owner/repo" {
		t.Fatalf("object source = %+v", g.Source)
	}
}

func TestSafeSegment(t *testing.T) {
	bad := []string{"", ".", "..", "../etc", "a/b", `a\b`, "../../root"}
	for _, n := range bad {
		if err := safeSegment(n); err == nil {
			t.Errorf("safeSegment(%q) should be rejected", n)
		}
	}
	for _, n := range []string{"hello", "my-plugin", "plugin_2"} {
		if err := safeSegment(n); err != nil {
			t.Errorf("safeSegment(%q) should be allowed: %v", n, err)
		}
	}
}

func TestInstallRejectsTraversalName(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// A marketplace whose plugin name is a path-traversal string.
	mp := filepath.Join(tmp, "market")
	if err := os.MkdirAll(filepath.Join(mp, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(mp, "p"), 0o755)
	os.WriteFile(filepath.Join(mp, ".claude-plugin", "marketplace.json"),
		[]byte(`{"name":"m","owner":{"name":"x"},"plugins":[{"name":"../../evil","source":"./p"}]}`), 0o644)
	if _, err := AddMarketplace(mp); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("../../evil"); err == nil {
		t.Fatal("Install should reject a traversal plugin name")
	}
}

func TestDeriveName(t *testing.T) {
	cases := map[string]string{
		"owner/repo":                      "repo",
		"owner/repo.git":                  "repo",
		"owner/repo@main":                 "repo",
		"https://github.com/a/b.git":      "b",
		"git@github.com:a/cool-tools.git": "cool-tools",
	}
	for in, want := range cases {
		if got := deriveName(in); got != want {
			t.Errorf("deriveName(%q) = %q, want %q", in, got, want)
		}
	}
}
