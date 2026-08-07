package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnsandboxedReachesOutside is the change: a local session can open the
// checkout next door. Being unable to was an obstacle, not a protection, when
// the person running it can read the file themselves anyway.
func TestUnsandboxedReachesOutside(t *testing.T) {
	outside := t.TempDir()
	sibling := filepath.Join(outside, "notes.txt")
	if err := os.WriteFile(sibling, []byte("hello from next door\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, _ := ws(t, map[string]string{"in.txt": "inside\n"})

	got, err := w.readFile(sibling)
	if err != nil {
		t.Fatalf("an unsandboxed session should read an absolute path: %v", err)
	}
	if !strings.Contains(got, "hello from next door") {
		t.Fatalf("content = %q", got)
	}
	// Relative paths still mean the workspace, so the common case is unchanged.
	if _, err := w.readFile("in.txt"); err != nil {
		t.Fatalf("a relative path should still resolve against the root: %v", err)
	}
	if _, err := w.resolve("../" + filepath.Base(outside)); err != nil {
		t.Fatalf("walking up should be allowed when unsandboxed: %v", err)
	}
}

// TestSandboxedStaysPut is the mode that keeps the old guarantee, and it is what
// every server-driven session runs with.
func TestSandboxedStaysPut(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	w, err := newWorkspace(dir, true)
	if err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{secret, "../escape", "/etc/passwd"} {
		if _, err := w.resolve(bad); err == nil {
			t.Errorf("a sandboxed session resolved %q", bad)
		}
	}
	if _, err := w.readFile(secret); err == nil {
		t.Error("a sandboxed session read a file outside its workspace")
	}
	// And it says why, so the model does not keep trying.
	if _, err := w.resolve("/etc/passwd"); err == nil || !strings.Contains(err.Error(), "sandboxed") {
		t.Errorf("the refusal should name the reason: %v", err)
	}
}

// TestPromptMatchesTheReach: a prompt that claims a confinement the session does
// not have teaches the model to refuse work it could do — which is exactly what
// was watched happening, a session declining to look at a sibling directory.
func TestPromptMatchesTheReach(t *testing.T) {
	loose := basePrompt([]string{"read", "bash"}, false)
	if strings.Contains(loose, "paths that escape it are\nrejected") {
		t.Error("an unsandboxed session should not be told it is confined")
	}
	if !strings.Contains(loose, "absolute path") {
		t.Errorf("it should know it can reach outside:\n%s", loose)
	}

	tight := basePrompt([]string{"read", "bash"}, true)
	if !strings.Contains(tight, "sandboxed") {
		t.Errorf("a sandboxed session should be told so:\n%s", tight)
	}
	if strings.Contains(tight, "absolute path") {
		t.Error("a sandboxed session should not be offered a reach it does not have")
	}
}
