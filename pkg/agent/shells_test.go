package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestBackgroundForeground verifies ctrl+b's mechanism: a running foreground
// bash command is converted into a registered background shell, returning a note
// to the model instead of blocking the loop.
func TestBackgroundForeground(t *testing.T) {
	a, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}

	resCh := make(chan string, 1)
	go func() {
		out, _ := a.runForegroundBash(context.Background(), "sleep 2; echo done")
		resCh <- out
	}()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case out := <-resCh:
			if !strings.Contains(out, "moved to background") {
				t.Fatalf("expected background note, got: %q", out)
			}
			if got := len(a.shells); got != 1 {
				t.Fatalf("expected 1 registered shell, got %d", got)
			}
			return
		case <-deadline:
			t.Fatal("command was never backgrounded")
		default:
			a.BackgroundForeground() // non-blocking; delivered once the run is waiting
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// TestForegroundBashCompletes checks the non-converted path still returns the
// command's output synchronously.
func TestForegroundBashCompletes(t *testing.T) {
	a, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, isErr := a.runForegroundBash(context.Background(), "echo hello")
	if isErr {
		t.Fatalf("unexpected error result: %q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected output, got: %q", out)
	}
	if len(a.shells) != 0 {
		t.Fatalf("no shell should be registered, got %d", len(a.shells))
	}
}
