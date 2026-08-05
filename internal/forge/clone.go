package forge

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Clone places a repository in dir, which becomes the repository's own root.
//
// Not `git clone`, which refuses a destination that is not empty — and the
// destination here is a session's workspace, a freshly provisioned volume that
// on ext4 already holds lost+found. Init, fetch, checkout is the same three
// things clone does and does not care what else is in the directory.
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
// And it is not left behind. The remote is the plain URL from the start, and
// the credentialed one is named only on the fetch, so nothing writes a token
// into .git/config on a volume that outlives the pod.
func Clone(ctx context.Context, dir, cloneURL, branch string) error {
	clean, withUser, token, err := splitCredential(cloneURL)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	env, cleanup, err := gitEnv(token)
	if err != nil {
		return err
	}
	defer cleanup()

	git := func(what string, args ...string) error {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w: %s", what, err, Redact(strings.TrimSpace(string(out))))
		}
		return nil
	}

	// The branch has to be known up front, where clone would have discovered it.
	// The server resolves it from the host's own listing, so this is the rare
	// path — a repository whose default branch the listing did not report.
	if branch == "" {
		if branch, err = defaultBranch(ctx, withUser, env); err != nil {
			return err
		}
	}
	// -b, so the initial branch is the one about to be checked out rather than
	// whatever this machine's init.defaultBranch says. Otherwise a repository on
	// "main" starts life on a "master" that never existed.
	if err := git("git init", "init", "-q", "-b", branch, dir); err != nil {
		return err
	}
	// The plain URL, because this is the one that gets written down. Fetching
	// names the credentialed one instead, which git records nowhere.
	if err := git("git remote add", "-C", dir, "remote", "add", "origin", clean); err != nil {
		return err
	}
	if err := git("git fetch", "-C", dir, "fetch", "--tags", "--",
		withUser, "+refs/heads/*:refs/remotes/origin/*"); err != nil {
		return err
	}
	// -B rather than a plain checkout: init already made the branch, and this
	// points it at what was fetched and fills the working tree in one step.
	if err := git("git checkout", "-C", dir, "checkout", "-B", branch,
		"refs/remotes/origin/"+branch); err != nil {
		return err
	}
	// So `git pull` on the branch knows where from, which init and a bare fetch
	// leave unset.
	if err := git("git branch", "-C", dir, "branch", "--set-upstream-to",
		"origin/"+branch, branch); err != nil {
		return err
	}
	return nil
}

// defaultBranch asks the remote what it considers HEAD.
//
// One extra round trip, only when the caller could not say. ls-remote --symref
// is the one way to learn this without a working copy to look at.
func defaultBranch(ctx context.Context, repoURL string, env []string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--symref", "--", repoURL, "HEAD")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git ls-remote: %w: %s", err, Redact(errText(err)))
	}
	for _, line := range strings.Split(string(out), "\n") {
		// "ref: refs/heads/main\tHEAD"
		rest, ok := strings.CutPrefix(line, "ref: ")
		if !ok {
			continue
		}
		ref, _, _ := strings.Cut(rest, "\t")
		if b := strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/"); b != "" {
			return b, nil
		}
	}
	return "", fmt.Errorf("the remote named no default branch")
}

// errText is whatever an ExitError captured on stderr, which is where git puts
// the reason.
func errText(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return strings.TrimSpace(string(ee.Stderr))
	}
	return ""
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
