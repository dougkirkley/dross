package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dougkirkley/ergon/internal/agent"
)

// TestCompactRecorder proves the recorder appends one JSONL line per record and
// tolerates concurrent writers (parallel worker subagents).
func TestCompactRecorder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compact-stats.jsonl")
	rec := newCompactRecorder(path)

	done := make(chan struct{})
	for i := 0; i < 5; i++ {
		go func() {
			rec(agent.CompactRecord{Hash: "aaa", Tool: "read", RawBytes: 9000, OutBytes: 800})
			done <- struct{}{}
		}()
	}
	for i := 0; i < 5; i++ {
		<-done
	}
	rec(agent.CompactRecord{Hash: "bbb", Tool: "bash", RawBytes: 7000, OutBytes: 500})

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 6 {
		t.Fatalf("want 6 lines, got %d: %q", len(lines), b)
	}
	// "aaa" recurs 5x → 4 redundant calls a cache would have served.
	if got := strings.Count(string(b), `"hash":"aaa"`); got != 5 {
		t.Fatalf("aaa count = %d, want 5", got)
	}
}
