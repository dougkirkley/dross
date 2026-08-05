package forge

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Clone places a repository in dir, which must not already exist.
//
// A subdirectory of the workspace rather than the workspace itself, because
// `git clone` refuses a destination that is not empty and a freshly provisioned
// volume is not: ext4 puts lost+found in it. The caller runs the session inside
// what this returns, so the repository is still what somebody lands in.
//
// cloneURL is the one CloneURL built: it carries the credential, which is what
// makes this work without a terminal to prompt at. Two things are done about
// that credential and both matter.
//
// It is never an argument. git is given the URL with only the username in it
// and asks for the password through GIT_ASKPASS, which reads it from this
// process's environment. A token in argv is readable by every process on the
// box — including, in a session pod, the shell commands the model later runs.
//
// And it is not left behind. git records the URL it cloned from in
// .git/config, so a credentialed clone writes the token to a file on a volume
// that outlives the pod; the remote is rewritten to the plain URL before this
// returns, which is also what makes the recorded remote something a person can
// read.
func Clone(ctx context.Context, dir, cloneURL, branch string) error {
	clean, withUser, token, err := splitCredential(cloneURL)
	if err != nil {
		return err
	}
	env, cleanup, err := gitEnv(token)
	if err != nil {
		return err
	}
	defer cleanup()

	args := []string{"clone"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	// "--" so a repository or branch that begins with a dash is a name and not
	// an option. Neither host allows one today; the guard costs nothing and does
	// not depend on that staying true.
	args = append(args, "--", withUser, dir)

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone: %w: %s", err, Redact(strings.TrimSpace(string(out))))
	}

	// The clone succeeded, so what is in .git/config is the URL with the
	// username in it. Rewriting it to the plain one leaves a remote anybody can
	// read and nobody can authenticate with.
	set := exec.CommandContext(ctx, "git", "-C", dir, "remote", "set-url", "origin", clean)
	set.Env = env
	if out, err := set.CombinedOutput(); err != nil {
		return fmt.Errorf("git remote set-url: %w: %s", err, Redact(strings.TrimSpace(string(out))))
	}
	return nil
}

// gitEnv is the environment every git call here runs with, plus the cleanup for
// the credential helper it may have written.
func gitEnv(token string) (env []string, cleanup func(), err error) {
	env = append(os.Environ(),
		// No prompting, ever. There is no terminal in a session pod, and a
		// credential that has stopped working would otherwise hang until the
		// context expires instead of failing with the reason.
		"GIT_TERMINAL_PROMPT=0",
		// And no credential helper from the machine's git config: on a
		// developer's laptop that is a keychain, which would answer with
		// somebody's personal credential for the same host.
		"GIT_CONFIG_NOSYSTEM=1",
	)
	if token == "" {
		return env, func() {}, nil
	}
	askpass, drop, err := askpassScript()
	if err != nil {
		return nil, nil, err
	}
	return append(env, "GIT_ASKPASS="+askpass, askpassVar+"="+token), drop, nil
}

// askpassVar is the variable the helper script reads the password from.
const askpassVar = "DROSS_GIT_TOKEN"

// askpassScript writes the helper git calls to ask for a password.
//
// A file rather than a value because that is the interface: GIT_ASKPASS names
// a program. It prints one environment variable and nothing else, so the
// secret travels in this process's environment and never through an argument
// list or the filesystem.
func askpassScript() (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "dross-askpass-*.sh")
	if err != nil {
		return "", nil, fmt.Errorf("git credential helper: %w", err)
	}
	name := f.Name()
	drop := func() { _ = os.Remove(name) }
	if _, err := f.WriteString("#!/bin/sh\nprintf '%s\\n' \"$" + askpassVar + "\"\n"); err != nil {
		f.Close()
		drop()
		return "", nil, fmt.Errorf("git credential helper: %w", err)
	}
	if err := f.Close(); err != nil {
		drop()
		return "", nil, fmt.Errorf("git credential helper: %w", err)
	}
	// Executable by this user only: anyone who can run it learns nothing, but
	// anyone who can rewrite it chooses what git executes.
	if err := os.Chmod(name, 0o700); err != nil {
		drop()
		return "", nil, fmt.Errorf("git credential helper: %w", err)
	}
	return name, drop, nil
}

// splitCredential takes a URL built by CloneURL apart again: the plain URL, the
// one with only the username in it, and the token.
//
// The two halves travel as one string — through a Secret, into the pod — so
// this is where they come back apart. A URL with no credential is not an error:
// a public repository clones without one.
func splitCredential(raw string) (clean, withUser, token string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", "", fmt.Errorf("clone URL: %s", Redact(err.Error()))
	}
	// A scheme rather than a host: the hosts send https URLs, and a test clones
	// over file://, which is authorityless by construction. What is being
	// caught here is a string that is not a URL at all, which would otherwise
	// reach git as a local path.
	if u.Scheme == "" {
		return "", "", "", fmt.Errorf("clone URL has no scheme")
	}
	user := u.User
	u.User = nil
	clean = u.String()
	if user == nil {
		return clean, clean, "", nil
	}
	token, _ = user.Password()
	u.User = url.User(user.Username())
	return clean, u.String(), token, nil
}

// credentialInURL matches the password half of a userinfo, keeping the scheme
// and username so what is left still reads as the URL it came from.
var credentialInURL = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://[^/\s:@]*):[^/\s@]*@`)

// Redact removes credentials from text that is about to be logged or returned.
//
// git quotes the URL it was given in most of its failures, and the URL is the
// one place a token is deliberately put. An error message is the most-copied
// text there is — into a terminal's scrollback, an issue, a chat — so the
// scrubbing happens here rather than at each place that prints one.
func Redact(s string) string {
	return credentialInURL.ReplaceAllString(s, "$1:***@")
}

// Branch creates a branch and checks it out.
//
// A session gets its own, so what it does is somewhere separate until somebody
// decides otherwise. Committing straight onto the default branch is the one
// outcome nobody asks for and the easy one to arrive at by accident.
//
// Only ever called on a fresh clone. -b rather than -B deliberately: a branch
// that already exists means this ran twice, and the forceful form would move it
// and throw away whatever had been committed on it.
func Branch(ctx context.Context, dir, name string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "checkout", "-b", name)
	env, cleanup, err := gitEnv("")
	if err != nil {
		return err
	}
	defer cleanup()
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout -b: %w: %s", err, Redact(strings.TrimSpace(string(out))))
	}
	return nil
}

// CloneDir is where a repository goes inside a session's workspace.
//
// Named for the repository rather than being the workspace root, because the
// root already exists and may already hold something. The session runs in here,
// so the nesting is not something anybody has to walk into.
func CloneDir(workspace, fullName string) string {
	return filepath.Join(workspace, RepoDirName(fullName))
}

// RepoDirName is the directory a repository clones into — the name git itself
// would pick, and the one it quotes in "Cloning into '<name>'".
func RepoDirName(fullName string) string {
	name := fullName
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(name, ".git")
	// A repository name is chosen by whoever owns the repository, and this ends
	// up as a path. Anything that is not plainly a name is dropped rather than
	// escaped, and a name that survives as nothing becomes "repo".
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		}
		return -1
	}, name)
	name = strings.TrimLeft(name, ".")
	if name == "" {
		name = "repo"
	}
	return name
}
