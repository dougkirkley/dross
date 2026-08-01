//go:build !linux

package agent

import "fmt"

// Landlock is a Linux LSM. On other platforms (developer machines) there is no
// equivalent unprivileged filesystem sandbox, so shell commands run confined
// only by the tool layer — which is why sandboxed sessions are a server-side,
// Linux-only deployment concern.

const sandboxArgv = "__sandbox-exec"

func landlockAvailable() bool { return false }

func runSandboxed(string, []string) error {
	return fmt.Errorf("sandboxed exec is only supported on Linux")
}
