package bench

// Temporary: writes a task's seed to $SEED_DIR for the VHS recording. Delete after.
//
//	SEED_TASK=read-a-big-log SEED_DIR=/tmp/x go test ./internal/bench -run SeedOut

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeedOut(t *testing.T) {
	dir, name := os.Getenv("SEED_DIR"), os.Getenv("SEED_TASK")
	if dir == "" || name == "" {
		t.Skip("set SEED_DIR and SEED_TASK")
	}
	for _, task := range DefaultTasks() {
		if task.Name != name {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for p, content := range task.Files {
			full := filepath.Join(dir, p)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("seeded %s into %s", name, dir)
		t.Logf("PROMPT: %s", task.Prompt)
		return
	}
	t.Fatalf("no task named %q", name)
}
