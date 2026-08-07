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

// TestWorkingSeedIsGreen is the other half, and the more expensive mistake.
//
// rename-symbol used to share the broken seed. The agent is told to verify by
// running the tests, so it ran them, found a failure it had not caused, and
// spent most of the run fixing a bug the task never mentioned — while the
// rename it was actually graded on had finished by turn seven.
func TestWorkingSeedIsGreen(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	for p, content := range workingPkg() {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the rename seed must start green, or the task measures the wrong thing: %v\n%s", err, out)
	}
}
