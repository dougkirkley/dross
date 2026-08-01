//go:build linux

package agent

import (
	"fmt"
	"os"
	"syscall"

	"github.com/landlock-lsm/go-landlock/landlock"
)

// Landlock confinement for shell commands.
//
// The file tools already refuse to leave the workspace, but bash only ever got
// a working directory — `cd ..` reached a sibling session's files. Landlock is
// the kernel's unprivileged filesystem sandbox, so it closes that without root,
// capabilities, or namespaces (all of which the pod deliberately lacks).
//
// It cannot be applied in the server: a ruleset covers every thread of the
// calling process and can never be relaxed, so restricting here would sandbox
// dross itself. Instead the agent re-executes its own binary in sandbox mode;
// that child applies the ruleset to itself and only then execs the real command.

// sandboxArgv is the hidden argv[1] that puts a dross process into sandbox mode.
const sandboxArgv = "__sandbox-exec"

// readOnlyPaths are the system locations a command still needs: interpreters,
// toolchains, certificates. They are read-only, so nothing here can be modified
// even though the paths remain visible.
var readOnlyPaths = []string{
	"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/opt",
}

// landlockAvailable reports whether the kernel supports Landlock at all, so a
// caller can refuse to run unconfined rather than silently doing so.
func landlockAvailable() bool {
	// V1 is the floor; every later ABI includes it. Restricting nothing is a
	// no-op ruleset, which succeeds iff Landlock is present and usable.
	return landlock.V1.RestrictPaths() == nil
}

// runSandboxed is the entry point for the re-executed child. It applies a
// ruleset allowing writes only under rwRoot, then execs argv. It never returns
// on success.
func runSandboxed(rwRoot string, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("sandbox: nothing to execute")
	}
	ro := make([]string, 0, len(readOnlyPaths))
	for _, p := range readOnlyPaths {
		if _, err := os.Stat(p); err == nil {
			ro = append(ro, p)
		}
	}
	rules := []landlock.Rule{
		landlock.RWDirs(rwRoot),
		// /dev and /tmp are needed by ordinary tooling; /tmp is a container
		// emptyDir, not shared with other sessions' workspaces.
		landlock.RWDirs("/dev", "/tmp"),
		landlock.RODirs(ro...),
	}
	// BestEffort downgrades to the newest ABI the kernel actually implements
	// rather than failing outright on an older one.
	if err := landlock.V5.BestEffort().RestrictPaths(rules...); err != nil {
		return fmt.Errorf("sandbox: %w", err)
	}
	path, err := lookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(path, argv, os.Environ())
}
