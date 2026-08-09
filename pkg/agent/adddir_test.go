package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twoTrees seeds a primary workspace and a sibling, each with a distinct marker
// so a hit can be attributed to the tree it came from.
func twoTrees(t *testing.T) (primary, other string) {
	t.Helper()
	primary, other = t.TempDir(), t.TempDir()
	write := func(dir, name, body string) {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(primary, "main.go", "package main\n\n// SharedSymbol lives here too.\n")
	write(other, "lib/api.go", "package lib\n\n// SharedSymbol is defined here.\nfunc SharedSymbol() {}\n")
	return primary, other
}

// TestGrepReachesAddedDirectories is the gap this closes. read and write always
// honoured an absolute path, so a second tree was editable but not findable —
// and search is how an agent locates anything, so the tree was effectively
// invisible.
func TestGrepReachesAddedDirectories(t *testing.T) {
	primary, other := twoTrees(t)
	ag, err := New(Config{Name: "t", Model: "m", Workdir: primary, AddDirs: []string{other}},
		&scriptedProvider{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := ag.ws.grep("SharedSymbol", grepOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "main.go") {
		t.Errorf("primary workspace hit missing:\n%s", out)
	}
	if !strings.Contains(out, filepath.Join(other, "lib/api.go")) {
		t.Errorf("added directory hit missing, or not named by absolute path:\n%s", out)
	}
}

// Hits are named by a path that actually opens: relative inside the primary
// workspace as before, absolute anywhere else. A path relative to the primary
// root would name a different file, or none, and cost a turn to discover.
func TestAddedDirectoryHitsAreAbsolute(t *testing.T) {
	primary, other := twoTrees(t)
	ag, _ := New(Config{Name: "t", Model: "m", Workdir: primary, AddDirs: []string{other}},
		&scriptedProvider{})

	out, err := ag.ws.glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "main.go") {
		t.Errorf("glob missed the primary tree:\n%s", out)
	}
	if !strings.Contains(out, other) {
		t.Errorf("glob missed the added tree:\n%s", out)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(line, "api.go") && !filepath.IsAbs(line) {
			t.Errorf("added-tree path is not absolute and will not open: %q", line)
		}
	}
}

// TestAddedDirectoriesAreWritableWhenSandboxed: Claude Code draws the line at
// "allowed or not" rather than read-only, and so does this. A sandboxed session
// still refuses everywhere else.
func TestAddedDirectoriesAreWritableWhenSandboxed(t *testing.T) {
	primary, other := twoTrees(t)
	forbidden := t.TempDir()
	w, err := newWorkspace(primary, true) // confined
	if err != nil {
		t.Fatal(err)
	}
	w.extra = []string{other}

	if _, err := w.writeFile(filepath.Join(other, "new.go"), "package lib\n"); err != nil {
		t.Fatalf("an added directory should be writable: %v", err)
	}
	if _, err := w.resolve(filepath.Join(forbidden, "x")); err == nil {
		t.Error("a sandboxed session should still refuse an unlisted directory")
	}
	if _, err := w.resolve("/etc/passwd"); err == nil {
		t.Error("a sandboxed session should still refuse /etc/passwd")
	}
	// And no traversal out of an added tree.
	if _, err := w.resolve(filepath.Join(other, "..", "..", "etc", "passwd")); err == nil {
		t.Error("traversal out of an added directory should be refused")
	}
}

// The model has to be told, or it will never look: the workspace map describes
// the primary root only, so an added tree is invisible until the prompt names it.
func TestAddedDirectoriesAreNamedInThePrompt(t *testing.T) {
	primary, other := twoTrees(t)
	ag, _ := New(Config{Name: "t", Model: "m", Workdir: primary, AddDirs: []string{other}},
		&scriptedProvider{})
	p := systemPrompt(ag.cfg, false, false, nil)
	if !strings.Contains(p, other) {
		t.Fatalf("the prompt never names the added directory:\n%s", p)
	}
	if !strings.Contains(p, "primary") {
		t.Error("the prompt should say which directory is primary")
	}
	// And says nothing at all when there are none.
	if got := addDirsGuidance(nil); got != "" {
		t.Errorf("guidance with no added dirs: %q", got)
	}
}

// A directory that does not exist is a typo, and saying so at startup beats
// silently searching one tree and reporting no matches.
func TestAddDirIsCheckedAtStartup(t *testing.T) {
	primary := t.TempDir()
	_, err := New(Config{Name: "t", Model: "m", Workdir: primary,
		AddDirs: []string{filepath.Join(primary, "nope")}}, &scriptedProvider{})
	if err == nil {
		t.Fatal("a missing add-dir should be an error, not a silent no-op")
	}
	if !strings.Contains(err.Error(), "add-dir") {
		t.Errorf("the error should name the flag: %v", err)
	}
}
