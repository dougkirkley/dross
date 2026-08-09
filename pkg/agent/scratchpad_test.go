package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScratchpadShape: per user, per workspace, per session — each part earns
// its place. The user root stops two people on a host colliding, the workspace
// directory makes a stray file traceable to what left it, and the session
// directory stops two runs on the same project overwriting each other.
func TestScratchpadShape(t *testing.T) {
	a := newScratchpad("/Users/doug/Projects/dross")
	b := newScratchpad("/Users/doug/Projects/dross")

	if !strings.Contains(a, fmt.Sprintf("dross-%d", os.Getuid())) {
		t.Errorf("no per-user component: %s", a)
	}
	if !strings.Contains(a, "-Users-doug-Projects-dross") {
		t.Errorf("workspace not legible in the path: %s", a)
	}
	if !strings.HasSuffix(a, "scratchpad") {
		t.Errorf("should end in scratchpad: %s", a)
	}
	if a == b {
		t.Errorf("two sessions on one workspace share a directory: %s", a)
	}
	// Different projects, different directories.
	if filepath.Dir(filepath.Dir(a)) == filepath.Dir(filepath.Dir(newScratchpad("/Users/doug/Projects/other"))) {
		t.Error("two workspaces share a directory")
	}
}

// TestScratchpadIsNotCreatedUntilUsed: a session that never writes a temp file
// should not leave an empty directory on every host it runs on.
func TestScratchpadIsNotCreatedUntilUsed(t *testing.T) {
	p := newScratchpad(t.TempDir())
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("scratchpad exists before anything wrote to it: %s", p)
	}
}

// TestSandboxedRunCanUseItsScratchpad is the case that makes this worth
// building. A confined session is refused every path outside the workspace, so
// without an exception it has nowhere legal for a temp file and writes into the
// repository instead.
func TestSandboxedRunCanUseItsScratchpad(t *testing.T) {
	dir := t.TempDir()
	scratch := filepath.Join(t.TempDir(), "scratchpad")
	w, err := newWorkspace(dir, true) // confined
	if err != nil {
		t.Fatal(err)
	}
	w.scratch = scratch

	// Still refused everywhere else outside the workspace.
	if _, err := w.resolve("/etc/passwd"); err == nil {
		t.Fatal("a sandboxed run should not resolve /etc/passwd")
	}
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	if _, err := w.resolve(outside); err == nil {
		t.Fatalf("a sandboxed run should not resolve %s", outside)
	}

	// But the scratchpad works, and writing to it actually lands.
	got, err := w.writeFile(filepath.Join(scratch, "summarise.py"), "print(1)\n")
	if err != nil {
		t.Fatalf("sandboxed write to the scratchpad: %v", err)
	}
	if !strings.Contains(got, "summarise.py") {
		t.Fatalf("write reported %q", got)
	}
	if b, err := os.ReadFile(filepath.Join(scratch, "summarise.py")); err != nil || string(b) != "print(1)\n" {
		t.Fatalf("file not written: %v %q", err, b)
	}
}

// Nothing may reach out of the scratchpad by traversal — it is one directory,
// not a hole in the sandbox.
func TestScratchpadIsNotAnEscapeHatch(t *testing.T) {
	dir := t.TempDir()
	base := t.TempDir()
	w, err := newWorkspace(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	w.scratch = filepath.Join(base, "scratchpad")
	for _, p := range []string{
		filepath.Join(base, "scratchpad", "..", "..", "etc", "passwd"),
		filepath.Join(base, "scratchpad-sibling", "x"),
		filepath.Join(base, "other"),
	} {
		if _, err := w.resolve(p); err == nil {
			t.Errorf("resolved %q, which is outside the scratchpad", p)
		}
	}
}

// An agent with no scratchpad configured keeps the old behaviour exactly:
// nothing outside the workspace, and nothing in the prompt about one.
func TestScratchpadCanBeTurnedOff(t *testing.T) {
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir(), Scratchpad: "-"},
		&scriptedProvider{})
	if err != nil {
		t.Fatal(err)
	}
	if ag.cfg.Scratchpad != "" {
		t.Fatalf("Scratchpad = %q, want empty", ag.cfg.Scratchpad)
	}
	if got := scratchpadGuidance(""); got != "" {
		t.Errorf("guidance with no scratchpad: %q", got)
	}
}

// And by default every agent gets one, named in its system prompt — a path the
// model is never told about is a path it will never use.
func TestScratchpadIsInThePrompt(t *testing.T) {
	dir := t.TempDir()
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, &scriptedProvider{})
	if err != nil {
		t.Fatal(err)
	}
	if ag.cfg.Scratchpad == "" {
		t.Fatal("no scratchpad was chosen")
	}
	p := systemPrompt(ag.cfg, false, false, nil)
	if !strings.Contains(p, ag.cfg.Scratchpad) {
		t.Fatalf("the prompt never names the scratchpad:\n%s", p)
	}
	if !strings.Contains(p, "version control") {
		t.Error("the prompt should say why not to use the workspace")
	}
}
