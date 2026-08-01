package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// A symlink inside the workspace must not become a way out of it. This needs no
// shell access to set up — a cloned repo can carry the link.
func TestSymlinkCannotEscapeWorkspace(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	w := &workspace{root: root}

	// A link to a file outside, and a link to the directory containing it.
	if err := os.Symlink(secret, filepath.Join(root, "leak")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"leak", "dir/id_rsa", "dir/newfile.txt"} {
		if _, err := w.resolve(p); err == nil {
			t.Errorf("resolve(%q) allowed an escape via symlink", p)
		}
	}

	// Reading and writing through the link must fail, not just resolve.
	if out, err := w.readFile("leak"); err == nil {
		t.Errorf("readFile followed the symlink and returned %q", out)
	}
	if _, err := w.writeFile("dir/planted.txt", "x"); err == nil {
		t.Error("writeFile wrote outside the workspace via a symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "planted.txt")); !os.IsNotExist(err) {
		t.Error("a file was created outside the workspace")
	}

	// Ordinary paths still work, including files that do not exist yet.
	if _, err := w.writeFile("normal.txt", "hello"); err != nil {
		t.Errorf("writeFile on a normal path failed: %v", err)
	}
	if _, err := w.resolve("nested/new/file.txt"); err != nil {
		t.Errorf("resolve of a not-yet-created path failed: %v", err)
	}
	// A symlink that stays inside the workspace is fine.
	if err := os.Symlink(filepath.Join(root, "normal.txt"), filepath.Join(root, "inside")); err == nil {
		if _, err := w.resolve("inside"); err != nil {
			t.Errorf("an internal symlink was rejected: %v", err)
		}
	}
}
