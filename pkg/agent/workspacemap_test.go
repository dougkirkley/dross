package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeNested writes a file at a workspace-relative path, creating the
// directories on the way — the sibling helper in prompt_workspace_test.go only
// handles a flat name.
func writeNested(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestWorkspaceMapListsTheFiles is the turn this saves: every benchmark task
// used to open with a glob to discover a five-file directory.
func TestWorkspaceMapListsTheFiles(t *testing.T) {
	dir := t.TempDir()
	writeNested(t, dir, "main.go", "package main\n")
	writeNested(t, dir, "internal/limiter/limiter.go", "package limiter\n")

	got := workspaceMap(dir)
	for _, want := range []string{"main.go", filepath.Join("internal", "limiter", "limiter.go")} {
		if !strings.Contains(got, want) {
			t.Errorf("map missing %s:\n%s", want, got)
		}
	}
	// It must not imply it knows what is in them.
	if strings.Contains(got, "package main") {
		t.Errorf("map should list files, not their contents:\n%s", got)
	}
	// And it must say it is a snapshot, or a model will trust it over its eyes.
	if !strings.Contains(got, "snapshot") {
		t.Errorf("map should be labelled as a snapshot:\n%s", got)
	}
}

// TestWorkspaceMapSkipsNoise: node_modules and .git would bury the signal and
// blow the budget.
func TestWorkspaceMapSkipsNoise(t *testing.T) {
	dir := t.TempDir()
	writeNested(t, dir, "main.go", "package main\n")
	writeNested(t, dir, "node_modules/left-pad/index.js", "module.exports=1\n")
	writeNested(t, dir, "vendor/x/y.go", "package x\n")

	got := workspaceMap(dir)
	if !strings.Contains(got, "main.go") {
		t.Fatalf("map lost the real file:\n%s", got)
	}
	for _, noise := range []string{"node_modules", "vendor"} {
		if strings.Contains(got, noise) {
			t.Errorf("map should skip %s:\n%s", noise, got)
		}
	}
}

// TestWorkspaceMapIsBounded keeps a monorepo from crowding out the rest of the
// prompt.
func TestWorkspaceMapIsBounded(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < mapEntries*3; i++ {
		writeNested(t, dir, filepath.Join("pkg", "f"+strings.Repeat("x", i%40)+string(rune('a'+i%26))+".go"), "x")
	}
	got := workspaceMap(dir)
	if len(got) > mapBudget*2 {
		t.Fatalf("map is %d bytes, want it bounded near %d", len(got), mapBudget)
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("a truncated map must say so:\n%s", got)
	}
}

// TestWorkspaceMapReportsGitState: the branch and whether the tree is dirty are
// the other two facts a session would otherwise spend a turn asking git for.
func TestWorkspaceMapReportsGitState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	writeNested(t, dir, "main.go", "package main\n")
	for _, args := range [][]string{
		{"init", "-b", "trunk"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"add", "."},
		{"commit", "-m", "first"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v\n%s", args, err, out)
		}
	}

	got := workspaceMap(dir)
	if !strings.Contains(got, "trunk") {
		t.Errorf("map should name the branch:\n%s", got)
	}
	if !strings.Contains(got, "clean") {
		t.Errorf("a clean tree should say so:\n%s", got)
	}

	writeNested(t, dir, "main.go", "package main // touched\n")
	if got := workspaceMap(dir); !strings.Contains(got, "uncommitted") {
		t.Errorf("a dirty tree should say so:\n%s", got)
	}
}

// TestWorkspaceMapEmptyDir: nothing to say, so say nothing rather than print an
// empty heading.
func TestWorkspaceMapEmptyDir(t *testing.T) {
	if got := workspaceMap(t.TempDir()); got != "" {
		t.Fatalf("empty workspace should produce no map, got:\n%s", got)
	}
	if got := workspaceMap(""); got != "" {
		t.Fatalf("no workdir should produce no map, got:\n%s", got)
	}
}
