//go:build integration

// Real-CLI integration tests. Build-tagged so they never compile into normal
// `go test`; run with:  DROSS_LIVE=1 go test -tags integration ./pkg/provider/claudecode/
package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dougkirkley/dross/pkg/agent"
	"github.com/dougkirkley/dross/pkg/provider"
)

// liveModel is the lowest Claude Code alias that reliably drives the XML tool
// protocol — haiku often won't emit tool calls, so these round-trip tests would
// be meaningless with it. Override with DROSS_LIVE_MODEL (e.g. "opus").
func liveModel() string {
	if m := os.Getenv("DROSS_LIVE_MODEL"); m != "" {
		return m
	}
	return "sonnet"
}

// skipUnlessLive gates the real-CLI tests behind DROSS_LIVE=1.
func skipUnlessLive(t *testing.T) {
	t.Helper()
	if os.Getenv("DROSS_LIVE") != "1" {
		t.Skip("set DROSS_LIVE=1 to run against the real claude CLI")
	}
}

// TestDrivenReadRoundTrip is the end-to-end integration test for the driven
// provider through the full agent loop: the model asks to read a file, dross
// executes the read, feeds the result back, and the final answer must contain
// the file's sentinel. This is the exact round-trip where tool I/O was getting
// corrupted, so a clean sentinel proves the stream isn't scrambled.
func TestDrivenReadRoundTrip(t *testing.T) {
	skipUnlessLive(t)
	dir := t.TempDir()
	const sentinel = "ZORP-4242-SENTINEL"
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("The secret code is "+sentinel+".\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prov, err := provider.New("claude-code", provider.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := agent.New(
		agent.Config{Name: "dross", Model: liveModel(), Workdir: dir},
		prov,
		agent.WithAllowedTools([]string{"read", "bash"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	res, err := ag.Run(ctx, "Read note.txt in the current directory and reply with ONLY the secret code value, nothing else.")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("turns=%d text=%q", res.Turns, res.Text)
	if !strings.Contains(res.Text, sentinel) {
		t.Fatalf("tool round-trip corrupted: final answer %q does not contain %q", res.Text, sentinel)
	}
}

// TestDrivenBashRoundTrip exercises the bash tool end-to-end: the model runs a
// shell command, dross executes it, and the real output must reach the answer.
// The earlier corruption showed up as garbled/blank command output, so this
// guards the bash path specifically.
func TestDrivenBashRoundTrip(t *testing.T) {
	skipUnlessLive(t)
	dir := t.TempDir()
	const sentinel = "BASHTOKEN-8891"
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte(sentinel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prov, err := provider.New("claude-code", provider.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := agent.New(
		agent.Config{Name: "dross", Model: liveModel(), Workdir: dir},
		prov,
		agent.WithAllowedTools([]string{"read", "bash"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	res, err := ag.Run(ctx, "Run the shell command `cat marker.txt` and reply with ONLY its exact output.")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("turns=%d text=%q", res.Turns, res.Text)
	if !strings.Contains(res.Text, sentinel) {
		t.Fatalf("bash round-trip corrupted: final answer %q does not contain %q", res.Text, sentinel)
	}
}

// TestDrivenWriteRoundTrip checks the write path: the model writes a file, dross
// executes it, and the file lands on disk with the exact content.
func TestDrivenWriteRoundTrip(t *testing.T) {
	skipUnlessLive(t)
	dir := t.TempDir()
	const sentinel = "WRITE-OK-7777"
	prov, err := provider.New("claude-code", provider.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := agent.New(
		agent.Config{Name: "dross", Model: liveModel(), Workdir: dir},
		prov,
		agent.WithAllowedTools([]string{"read", "write", "bash"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	if _, err := ag.Run(ctx, "Create a file named out.txt containing exactly this line and nothing else: "+sentinel); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("expected out.txt to be written: %v", err)
	}
	if !strings.Contains(string(got), sentinel) {
		t.Fatalf("write corrupted: out.txt = %q, want it to contain %q", string(got), sentinel)
	}
}
