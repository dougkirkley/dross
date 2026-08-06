package bench

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSeedIsBrokenAsDesigned guards the multi-file tasks: their whole value is
// that the shipped test fails for one findable reason. A seed that quietly
// starts passing would score every agent as a success without any of them
// having done anything.
func TestSeedIsBrokenAsDesigned(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	for p, content := range multiFilePkg() {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) ([]byte, error) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		return cmd.CombinedOutput()
	}
	if out, err := run("go", "test", "./..."); err == nil {
		t.Fatalf("seed passes its own test, so the task grades nothing:\n%s", out)
	}
	// Broken behaviour, not broken code: the agent has to read to find it.
	if out, err := run("go", "vet", "./..."); err != nil {
		t.Fatalf("seed should compile and vet clean: %v\n%s", err, out)
	}
}
