package forge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// origin makes a repository with one commit in it and returns its path, so the
// clone under test is a real git clone rather than a stubbed one.
func origin(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "--initial-branch=main", ".")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "first")
	run("branch", "topic")
	return dir
}

func TestCloneChecksOutTheRepository(t *testing.T) {
	src := origin(t)
	dest := filepath.Join(t.TempDir(), "dross")

	if err := Clone(context.Background(), dest, "file://"+src, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "README.md")); err != nil {
		t.Errorf("the clone has no working tree: %v", err)
	}
}

func TestCloneTakesTheAskedForBranch(t *testing.T) {
	src := origin(t)
	dest := filepath.Join(t.TempDir(), "dross")

	if err := Clone(context.Background(), dest, "file://"+src, "topic"); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", dest, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "topic" {
		t.Errorf("checked out %q, want the branch that was asked for", got)
	}
}

// The credential is what makes a private clone work and is the last thing that
// should survive it: .git/config sits on a volume that outlives the pod.
func TestCloneLeavesNoCredentialBehind(t *testing.T) {
	src := origin(t)
	dest := filepath.Join(t.TempDir(), "dross")

	// A file:// URL ignores the userinfo, which is exactly what makes it usable
	// here: the credential goes through the whole path without needing a server
	// that checks it.
	if err := Clone(context.Background(), dest, "file://x-access-token:gho_secret@"+src, ""); err != nil {
		t.Fatal(err)
	}
	cfg, err := os.ReadFile(filepath.Join(dest, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), "gho_secret") {
		t.Errorf("the token was written to .git/config:\n%s", cfg)
	}
	if !strings.Contains(string(cfg), "file://"+src) {
		t.Errorf("the remote is not the plain URL:\n%s", cfg)
	}
}

// A clone that fails quotes the URL it was given, and that URL is where the
// token is. The error is the most-copied text there is.
func TestCloneErrorCarriesNoToken(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "dross")
	err := Clone(context.Background(), dest, "file://x-access-token:gho_secret@/no/such/repository", "")
	if err == nil {
		t.Fatal("cloning a repository that does not exist succeeded")
	}
	if strings.Contains(err.Error(), "gho_secret") {
		t.Errorf("the failure quoted the token: %v", err)
	}
}

func TestRedact(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"https://x-access-token:gho_abc@github.com/a/b.git",
			"https://x-access-token:***@github.com/a/b.git"},
		{"https://oauth2:glpat-xyz@gitlab.com/a/b.git",
			"https://oauth2:***@gitlab.com/a/b.git"},
		// Two in one message: git repeats the URL when a clone fails.
		{"fatal: https://u:p@h/a failed, retry https://u:q@h/a",
			"fatal: https://u:***@h/a failed, retry https://u:***@h/a"},
		// Nothing to hide, and nothing to mangle either.
		{"https://github.com/a/b.git", "https://github.com/a/b.git"},
		{"could not reach doug@example.com", "could not reach doug@example.com"},
		{"remote: Repository not found", "remote: Repository not found"},
	} {
		if got := Redact(c.in); got != c.want {
			t.Errorf("Redact(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSplitCredential(t *testing.T) {
	clean, withUser, token, err := splitCredential("https://x-access-token:gho_abc@github.com/a/b.git")
	if err != nil {
		t.Fatal(err)
	}
	if clean != "https://github.com/a/b.git" {
		t.Errorf("clean URL %q", clean)
	}
	// The username stays in the URL: git only asks the askpass helper for a
	// password when it already knows who is asking.
	if withUser != "https://x-access-token@github.com/a/b.git" {
		t.Errorf("URL for git %q", withUser)
	}
	if token != "gho_abc" {
		t.Errorf("token %q", token)
	}

	// A public repository has no credential and is not an error.
	clean, withUser, token, err = splitCredential("https://github.com/a/b.git")
	if err != nil || token != "" || clean != "https://github.com/a/b.git" || withUser != clean {
		t.Errorf("public URL: %q %q %q %v", clean, withUser, token, err)
	}

	if _, _, _, err := splitCredential("not a url at all"); err == nil {
		t.Error("a URL naming no host was accepted")
	}
}

// The workspace already exists and may already hold something — a session
// volume arrives with lost+found on ext4 — which is why the clone goes in a
// directory of its own beneath it.
func TestCloneDirIsBeneathAWorkspaceThatIsNotEmpty(t *testing.T) {
	src := origin(t)
	ws := t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, "lost+found"), 0o700); err != nil {
		t.Fatal(err)
	}

	dir := CloneDir(ws, "doug/dross")
	if err := Clone(context.Background(), dir, "file://"+src, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Errorf("no working tree in %s: %v", dir, err)
	}
	// git clone's own doing, and the reason for using it: the branch, the
	// upstream and the tags all come for free.
	up, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "main@{upstream}").Output()
	if err != nil {
		t.Fatalf("no upstream: %v", err)
	}
	if got := strings.TrimSpace(string(up)); got != "origin/main" {
		t.Errorf("upstream is %q", got)
	}
}

func TestCloneDir(t *testing.T) {
	for _, c := range []struct{ full, want string }{
		{"doug/dross", "dross"},
		{"doug/dross.git", "dross"},
		{"group/sub/project", "project"},
		// A repository name is chosen by whoever owns the repository and this
		// becomes a path.
		{"doug/../../etc", "etc"},
		{"doug/..", "repo"},
		{"doug/", "repo"},
	} {
		if got := CloneDir("/workspace", c.full); got != filepath.Join("/workspace", c.want) {
			t.Errorf("CloneDir(%q) = %q, want %q", c.full, got, filepath.Join("/workspace", c.want))
		}
	}
}
