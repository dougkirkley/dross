package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// lookPath resolves a command against PATH, so the sandbox child can exec it.
func lookPath(name string) (string, error) {
	if strings.Contains(name, string(filepath.Separator)) {
		return name, nil
	}
	return exec.LookPath(name)
}

// MaybeRunSandbox handles the re-exec of dross into sandbox mode. Call it first
// thing in main: when the process was started as the sandbox helper it applies
// the ruleset and execs the real command, never returning.
//
// Usage: dross __sandbox-exec <rw-root> <cmd> [args...]
func MaybeRunSandbox() error {
	if len(os.Args) < 4 || os.Args[1] != sandboxArgv {
		return nil
	}
	return runSandboxed(os.Args[2], os.Args[3:])
}

var (
	sandboxWarnOnce sync.Once
	sandboxUsable   bool
)

// sandboxExec wraps argv so it runs confined to rwRoot, or returns it unchanged
// when confinement is unavailable. notify is called once if the kernel cannot
// provide it, so an unconfined run is visible rather than silent.
func sandboxExec(rwRoot string, argv []string, notify func(string)) []string {
	sandboxWarnOnce.Do(func() {
		sandboxUsable = landlockAvailable()
		if !sandboxUsable && notify != nil {
			notify("shell commands are not filesystem-confined: this kernel has no usable Landlock support")
		}
	})
	if !sandboxUsable || rwRoot == "" {
		return argv
	}
	self, err := os.Executable()
	if err != nil {
		return argv
	}
	return append([]string{self, sandboxArgv, rwRoot}, argv...)
}

// sandboxArgv confines a command to this workspace when the session is
// sandboxed. Unsandboxed sessions (local CLI, device relay) run in the user's
// own environment by design and are left alone.
func (w *workspace) sandboxArgv(argv []string, notify func(string)) []string {
	if w == nil || w.env == nil {
		return argv // not a sandboxed session
	}
	return sandboxExec(w.root, argv, notify)
}

// sandboxWarn surfaces a missing-confinement warning to the caller once.
func (a *Agent) sandboxWarn(msg string) { a.emit("error", msg) }
