package bench

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestBigTreeIsBrokenInExactlyOnePlace guards the shape the task depends on:
// the tree must compile, every package's own test must pass, and the only
// failure must be the registry total. A seed that failed in several places
// would let an agent score by fixing any of them.
func TestBigTreeIsBrokenInExactlyOnePlace(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	files := bigTree()
	if len(files) < 40 {
		t.Fatalf("tree has %d files; the point of it is being too big to just read", len(files))
	}
	for p, content := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) ([]byte, error) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		return cmd.CombinedOutput()
	}
	if out, err := run("go", "vet", "./..."); err != nil {
		t.Fatalf("the tree must compile: %v\n%s", err, out)
	}
	out, err := run("go", "test", "./...")
	if err == nil {
		t.Fatalf("seed passes, so the task grades nothing:\n%s", out)
	}
	// Exactly one failing package, and it is the registry — not the component
	// that holds the bug, which is the whole difficulty of the task.
	if n := strings.Count(string(out), "FAIL\tbench"); n != 1 {
		t.Fatalf("want exactly one failing package, got %d:\n%s", n, out)
	}
	if strings.Contains(string(out), "FAIL\tbench/internal/") {
		t.Fatalf("a component's own test failed; the bug should only surface in the total:\n%s", out)
	}

	// And correcting the documented component fixes it — proving the task is
	// solvable the way the prompt describes.
	broken := filepath.Join(dir, "internal", brokenPart, brokenPart+".go")
	body, err := os.ReadFile(broken)
	if err != nil {
		t.Fatal(err)
	}
	var rate int
	if _, err := fmt.Sscanf(docRate(string(body)), "%d", &rate); err != nil {
		t.Fatalf("could not read the documented rate: %v", err)
	}
	fixed := strings.Replace(string(body),
		fmt.Sprintf("return %d", rate+brokenBy), fmt.Sprintf("return %d", rate), 1)
	if fixed == string(body) {
		t.Fatal("could not apply the intended fix")
	}
	if err := os.WriteFile(broken, []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run("go", "test", "./..."); err != nil {
		t.Fatalf("matching the code to its documentation should fix it: %v\n%s", err, out)
	}
}

// docRate pulls the number out of a package's "Rate is N." comment.
func docRate(src string) string {
	const marker = "// Rate is "
	i := strings.Index(src, marker)
	if i < 0 {
		return ""
	}
	rest := src[i+len(marker):]
	return strings.TrimSuffix(strings.SplitN(rest, "\n", 2)[0], ".")
}
