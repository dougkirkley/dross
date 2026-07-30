//go:build integration

// Real-CLI integration tests. Build-tagged so they never compile into normal
// `go test`; run with:  DROSS_LIVE=1 go test -tags integration ./pkg/provider/claudecode/
package claudecode

import (
	"context"
	"fmt"
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

// TestDrivenManyTurnsNoBleed stresses the driven session across many turns: a
// dozen files, each with a unique sentinel, read one-by-one so the --resume
// session state (where the earlier corruption came from) accumulates. Every
// sentinel must come back exactly, with none crossed between files.
func TestDrivenManyTurnsNoBleed(t *testing.T) {
	skipUnlessLive(t)
	dir := t.TempDir()
	const n = 12
	sentinels := make([]string, n)
	for i := 0; i < n; i++ {
		sentinels[i] = fmt.Sprintf("SENT-%02d-%s", i, strings.Repeat("x", 8))
		name := fmt.Sprintf("f%02d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("code: "+sentinels[i]+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prov, err := provider.New("claude-code", provider.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := agent.New(
		agent.Config{Name: "dross", Model: liveModel(), Workdir: dir},
		prov,
		agent.WithConversation(), // keep the session across Run calls (the --resume path)
		agent.WithAllowedTools([]string{"read", "bash"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		name := fmt.Sprintf("f%02d.txt", i)
		res, err := ag.Run(ctx, "Read "+name+" and reply with ONLY the code value.")
		cancel()
		if err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
		if !strings.Contains(res.Text, sentinels[i]) {
			t.Fatalf("turn %d (%s): answer %q missing %q — tool I/O bled/corrupted", i, name, res.Text, sentinels[i])
		}
		// Guard against another file's sentinel leaking into this answer.
		for j := 0; j < n; j++ {
			if j != i && strings.Contains(res.Text, sentinels[j]) {
				t.Fatalf("turn %d (%s): answer %q leaked %s's sentinel", i, name, res.Text, fmt.Sprintf("f%02d.txt", j))
			}
		}
		t.Logf("turn %d ok: %s", i, name)
	}
}

// TestDrivenConcurrentNoBleed runs several driven agents at once (mimicking the
// background agents / delegation that were live when the corruption appeared).
// Concurrent `claude` processes must not cross-contaminate: each agent's answer
// must contain only its own sentinel. Cross-bleed here reproduces the bug.
func TestDrivenConcurrentNoBleed(t *testing.T) {
	skipUnlessLive(t)
	dir := t.TempDir()
	const k = 4
	sentinels := make([]string, k)
	for i := 0; i < k; i++ {
		sentinels[i] = fmt.Sprintf("CONC-%02d-%s", i, strings.Repeat("z", 10))
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("c%02d.txt", i)), []byte("value: "+sentinels[i]+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prov, err := provider.New("claude-code", provider.Config{})
	if err != nil {
		t.Fatal(err)
	}
	type out struct {
		i    int
		text string
		err  error
	}
	results := make(chan out, k)
	for i := 0; i < k; i++ {
		go func(i int) {
			ag, err := agent.New(
				agent.Config{Name: fmt.Sprintf("dross-%d", i), Model: liveModel(), Workdir: dir},
				prov, agent.WithAllowedTools([]string{"read", "bash"}),
			)
			if err != nil {
				results <- out{i, "", err}
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			r, err := ag.Run(ctx, fmt.Sprintf("Read c%02d.txt and reply with ONLY the value.", i))
			if err != nil {
				results <- out{i, "", err}
				return
			}
			results <- out{i, r.Text, nil}
		}(i)
	}
	for r := 0; r < k; r++ {
		o := <-results
		if o.err != nil {
			t.Fatalf("agent %d: %v", o.i, o.err)
		}
		if !strings.Contains(o.text, sentinels[o.i]) {
			t.Fatalf("agent %d: answer %q missing its own sentinel %q", o.i, o.text, sentinels[o.i])
		}
		for j := 0; j < k; j++ {
			if j != o.i && strings.Contains(o.text, sentinels[j]) {
				t.Fatalf("agent %d: answer %q leaked agent %d's sentinel — concurrent sessions cross-contaminated", o.i, o.text, j)
			}
		}
		t.Logf("agent %d ok", o.i)
	}
}

// TestDrivenBashRedirects mirrors the exact shape that corrupted in the wild: a
// grouped command with a redirect and 2>&1, then reading the file back. If the
// driven CLI HTML-escapes >, &, etc in the tool arg and dross doesn't decode it,
// the redirect silently breaks and the read returns garbage.
func TestDrivenBashRedirects(t *testing.T) {
	skipUnlessLive(t)
	dir := t.TempDir()
	const sentinel = "REDIR-5150-OK"
	prov, err := provider.New("claude-code", provider.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := agent.New(
		agent.Config{Name: "dross", Model: liveModel(), Workdir: dir},
		prov, agent.WithAllowedTools([]string{"read", "bash"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	res, err := ag.Run(ctx, `Run exactly this shell command: { echo "`+sentinel+`"; echo "second"; } > probe.txt 2>&1 — then read probe.txt and reply with ONLY its first line.`)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("turns=%d text=%q", res.Turns, res.Text)
	// The file must have been created by the redirect and contain the sentinel.
	got, rerr := os.ReadFile(filepath.Join(dir, "probe.txt"))
	if rerr != nil {
		t.Fatalf("probe.txt not created — redirect likely broke (escaped '>'): %v", rerr)
	}
	if !strings.Contains(string(got), sentinel) {
		t.Fatalf("probe.txt = %q, want it to contain %q", string(got), sentinel)
	}
	if !strings.Contains(res.Text, sentinel) {
		t.Fatalf("answer %q missing %q", res.Text, sentinel)
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
