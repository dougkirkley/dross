package agent

import (
	"strings"
	"sync"
	"testing"
)

// Unsandboxed sessions (local CLI, device relay) run in the user's own
// environment on purpose and must not be wrapped.
func TestUnsandboxedSessionsAreNotWrapped(t *testing.T) {
	w := &workspace{root: "/workspace/ses_x"} // env nil = not sandboxed
	argv := []string{"bash", "-c", "echo hi"}
	got := w.sandboxArgv(argv, nil)
	if len(got) != 3 || got[0] != "bash" {
		t.Errorf("unsandboxed command was wrapped: %v", got)
	}
}

// A sandboxed session gets the re-exec wrapper, carrying its own root — the
// wrapper is what stops `cd ..` reaching a sibling session.
func TestSandboxedSessionsAreWrapped(t *testing.T) {
	if !landlockAvailable() {
		t.Skip("kernel has no Landlock; wrapping is skipped by design")
	}
	w := &workspace{root: "/workspace/ses_x", env: []string{"HOME=/workspace/ses_x"}}
	got := w.sandboxArgv([]string{"bash", "-c", "echo hi"}, nil)
	if len(got) < 4 || got[1] != sandboxArgv || got[2] != w.root {
		t.Fatalf("expected a sandbox re-exec for this root, got %v", got)
	}
	if strings.Join(got[3:], " ") != "bash -c echo hi" {
		t.Errorf("original command mangled: %v", got[3:])
	}
}

// If the kernel cannot confine, that must be visible rather than silent.
func TestMissingLandlockIsReported(t *testing.T) {
	sandboxWarnOnce = sync.Once{}
	var warned string
	w := &workspace{root: "/tmp/x", env: []string{"HOME=/tmp/x"}}
	_ = w.sandboxArgv([]string{"bash"}, func(m string) { warned = m })
	if !landlockAvailable() && warned == "" {
		t.Error("no warning emitted when confinement is unavailable")
	}
}
