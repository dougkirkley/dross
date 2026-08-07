package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dougkirkley/dross/pkg/provider"
)

// fakeCLI stands in for `claude -p --input-format stream-json`. It counts its
// own starts and numbers its turns, so a test can tell one process serving
// three turns from three processes serving one each — which is the whole point
// of the change and is invisible from the responses otherwise.
const fakeCLI = `#!/bin/sh
echo "$@" >> "$DROSS_TEST_ARGS"
echo start >> "$DROSS_TEST_STARTS"
n=0
while IFS= read -r line; do
  n=$((n+1))
  echo '{"type":"system","session_id":"fake-session"}'
  echo '{"type":"assistant","message":{"content":[{"type":"text","text":"turn '"$n"'"}],"usage":{"input_tokens":10,"output_tokens":2}}}'
  echo '{"type":"result","result":"turn '"$n"'","duration_api_ms":7,"usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":5}}'
done
`

func fakeSession(t *testing.T) (*session, func() int, func() string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte(fakeCLI), 0o755); err != nil {
		t.Fatal(err)
	}
	starts := filepath.Join(dir, "starts")
	argsFile := filepath.Join(dir, "args")
	t.Setenv("DROSS_TEST_STARTS", starts)
	t.Setenv("DROSS_TEST_ARGS", argsFile)

	p := &prov{bin: bin}
	s := p.NewSession(provider.SessionConfig{Model: "sonnet"}).(*session)
	t.Cleanup(func() { _ = s.Close() })

	countStarts := func() int {
		b, err := os.ReadFile(starts)
		if err != nil {
			return 0
		}
		return len(strings.Fields(string(b)))
	}
	readArgs := func() string {
		b, _ := os.ReadFile(argsFile)
		return string(b)
	}
	return s, countStarts, readArgs
}

// TestOneProcessServesEveryTurn is the change. Three turns used to mean three
// `claude` processes, each rejoining the transcript with --resume and each
// throwing the prompt cache away; now they share one.
func TestOneProcessServesEveryTurn(t *testing.T) {
	s, starts, args := fakeSession(t)
	ctx := context.Background()

	for i, want := range []string{"turn 1", "turn 2", "turn 3"} {
		resp, err := s.Send(ctx, provider.Input{Text: "hello"})
		if err != nil {
			t.Fatalf("turn %d: %v", i+1, err)
		}
		if resp.Text != want {
			// A restarted child would number every turn 1.
			t.Fatalf("turn %d: text = %q, want %q", i+1, resp.Text, want)
		}
	}
	if n := starts(); n != 1 {
		t.Errorf("the CLI started %d times, want 1", n)
	}
	if got := args(); strings.Contains(got, "--resume") {
		t.Errorf("a persistent process should never resume: %q", got)
	}
	if got := args(); !strings.Contains(got, "--input-format stream-json") {
		t.Errorf("turns are written to stdin, so the CLI must be told to read them: %q", got)
	}
}

// TestReportsUsageAndAPITime: the result event carries what the model itself
// cost and how long it took, and both have to survive the new read loop —
// APITime is what keeps the provider's own overhead out of the agent's model
// column.
func TestReportsUsageAndAPITime(t *testing.T) {
	s, _, _ := fakeSession(t)
	resp, err := s.Send(context.Background(), provider.Input{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.APITime != 7*time.Millisecond {
		t.Errorf("APITime = %v, want 7ms", resp.APITime)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.CacheReadTokens != 5 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if s.sessionID != "fake-session" {
		t.Errorf("sessionID = %q", s.sessionID)
	}
}

// TestCloseIsFinal: the process is shared, so a Send after Close must fail
// rather than quietly start a second conversation with no history in it.
func TestCloseIsFinal(t *testing.T) {
	s, starts, _ := fakeSession(t)
	if _, err := s.Send(context.Background(), provider.Input{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close should be idempotent: %v", err)
	}
	if _, err := s.Send(context.Background(), provider.Input{Text: "again"}); err == nil {
		t.Fatal("a Send after Close should fail")
	}
	if n := starts(); n != 1 {
		t.Errorf("Close then Send started the CLI again (%d starts)", n)
	}
}

// TestEmptyInputCostsNothing: an empty turn must not reach the child at all,
// because the CLI would answer it and the transcript would carry a turn nobody
// asked for.
func TestEmptyInputCostsNothing(t *testing.T) {
	s, starts, _ := fakeSession(t)
	resp, err := s.Send(context.Background(), provider.Input{Text: "   "})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != provider.StopEndTurn {
		t.Errorf("StopReason = %v", resp.StopReason)
	}
	if n := starts(); n != 0 {
		t.Errorf("an empty turn started the CLI (%d starts)", n)
	}
}

// TestCancelledTurnReportsCancellation: the turn is cancelled, not the process.
// The caller gets its context error back and the session stays usable.
func TestCancelledTurnReportsCancellation(t *testing.T) {
	s, _, _ := fakeSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Send(ctx, provider.Input{Text: "hello"}); err == nil {
		t.Fatal("a cancelled turn should report the cancellation")
	} else if err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, err := s.Send(context.Background(), provider.Input{Text: "hello"}); err != nil {
		t.Fatalf("the session should survive a cancelled turn: %v", err)
	}
}

// TestDeadChildFailsFast: once the CLI is gone, every later turn says so
// instead of blocking forever on a pipe with nothing behind it.
func TestDeadChildFailsFast(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	// Exits immediately without answering.
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &prov{bin: bin}
	s := p.NewSession(provider.SessionConfig{Model: "sonnet"}).(*session)
	defer s.Close()

	first, err := s.Send(context.Background(), provider.Input{Text: "hello"})
	if err == nil {
		t.Fatalf("a child that never answers should be an error, got %+v", first)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.Send(context.Background(), provider.Input{Text: "again"})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a later turn against a dead child should fail")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a later turn blocked instead of failing fast")
	}
}
