package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The scratchpad: somewhere to put a file that is not part of the project.
//
// A run often wants one — a script to summarise a log, a fixture to reproduce a
// bug, an intermediate dump too big to hold in context. Without a named place
// for it, both available options are bad. Writing into the workspace puts
// throwaway files in somebody's repository, where they show up in git status
// and get committed or cleaned up by hand. Writing to /tmp works only until the
// session is sandboxed, and then resolve() rejects it — correctly, since it is
// outside the workspace — leaving the model with nowhere legal to put anything
// and the repo as its only option.
//
// So the path is handed to the model explicitly, created on demand, and allowed
// through the sandbox by exception rather than by widening it. The shape copies
// the Claude Code CLI's, because it is a good one: a per-user root so two people
// on a host do not collide, a per-workspace directory so it is obvious what a
// stray file belongs to, and a per-session directory so concurrent runs on the
// same project cannot overwrite each other's work.
//
//	/private/tmp/dross-501/-Users-doug-Projects-dross/a3f9c21b/scratchpad

// scratchRoot is where per-user scratch directories live.
//
// "/tmp" rather than os.TempDir(): on macOS the latter is a private
// per-process path under /var/folders that nobody can find afterwards, and
// /tmp is the symlink to /private/tmp that people actually look in. Windows
// has no /tmp, so it keeps the platform's own answer.
func scratchRoot() string {
	if runtime.GOOS == "windows" {
		return os.TempDir()
	}
	return "/tmp"
}

// workspaceSlug turns an absolute workspace path into one directory name.
//
// Slashes become dashes, so /Users/doug/Projects/dross reads as
// -Users-doug-Projects-dross: still legible at a glance, which matters when
// the question is "what left this here".
func workspaceSlug(workdir string) string {
	abs, err := filepath.Abs(workdir)
	if err != nil {
		abs = workdir
	}
	slug := strings.ReplaceAll(filepath.ToSlash(abs), "/", "-")
	slug = strings.ReplaceAll(slug, ":", "") // windows drive letters
	if slug == "" {
		return "workspace"
	}
	return slug
}

// newScratchpad returns this run's scratch directory. It is not created here —
// a session that never writes a temp file should not leave an empty directory
// behind on every host it runs on.
func newScratchpad(workdir string) string {
	b := make([]byte, 4)
	id := "session"
	if _, err := rand.Read(b); err == nil {
		id = hex.EncodeToString(b)
	}
	return filepath.Join(scratchRoot(),
		fmt.Sprintf("dross-%d", os.Getuid()),
		workspaceSlug(workdir), id, "scratchpad")
}

// underScratchpad reports whether abs is inside the run's scratch directory.
//
// Compared after resolving symlinks on both sides, because /tmp is itself a
// symlink to /private/tmp on macOS: a lexical compare would say the model's
// own scratch path is outside its own scratch path.
func underScratchpad(scratch, abs string) bool {
	if scratch == "" {
		return false
	}
	real := func(p string) string {
		// Resolve the deepest part that exists; a file about to be created has
		// no symlink of its own to follow.
		for cur := filepath.Clean(p); ; cur = filepath.Dir(cur) {
			if r, err := filepath.EvalSymlinks(cur); err == nil {
				return filepath.Join(r, strings.TrimPrefix(filepath.Clean(p), cur))
			}
			if cur == "/" || cur == "." || cur == filepath.Dir(cur) {
				return filepath.Clean(p)
			}
		}
	}
	rel, err := filepath.Rel(real(scratch), real(abs))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// scratchpadGuidance tells the model where to put things that are not part of
// the project. Worded as a default rather than a suggestion: the failure it
// prevents is a repository with somebody's one-off script committed into it.
func scratchpadGuidance(dir string) string {
	if dir == "" {
		return ""
	}
	return "\n\n## Scratchpad\n\n" +
		"Put temporary files here, not in the workspace and not in /tmp:\n\n    " + dir +
		"\n\nIt is created on demand and is yours alone for this session. Use it for one-off " +
		"scripts, intermediate output, scratch data, anything you need to write down but that is " +
		"not part of the project. A throwaway file written into the workspace ends up in the " +
		"user's version control, which is theirs to clean up rather than yours to leave. " +
		"Files the task actually asks for still go in the workspace."
}
