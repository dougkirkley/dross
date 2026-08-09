package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

// failingTestLog is the shape that used to trigger a blocking model call on
// nearly every run: a long, mostly boring log whose whole point is the handful
// of lines with FAIL in them.
func failingTestLog() string {
	var b strings.Builder
	b.WriteString("=== RUN   TestEverything\n")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "    ok  \tpkg/thing/sub%d\t0.0%ds\n", i, i%9)
	}
	b.WriteString("--- FAIL: TestApply (0.00s)\n")
	b.WriteString("    limiter_test.go:13: Apply(0) = 10, want 1\n")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "    ok  \tpkg/other/sub%d\t0.0%ds\n", i, i%9)
	}
	b.WriteString("FAIL\nFAIL\tbench\t0.142s\nFAIL\n")
	return b.String()
}

// TestElideKeepsTheFailure is the whole point: the answer survives, the
// thousand passing lines do not, and no model was asked.
func TestElideKeepsTheFailure(t *testing.T) {
	log := failingTestLog()
	out, ok := elide(log, defaultCompactThreshold)
	if !ok {
		t.Fatal("a failing test log is exactly what elide is for")
	}
	if len(out) >= defaultCompactThreshold {
		t.Fatalf("elided output is %d bytes, over the %d budget", len(out), defaultCompactThreshold)
	}
	for _, want := range []string{
		"--- FAIL: TestApply",
		"Apply(0) = 10, want 1",
		"=== RUN   TestEverything", // the head
		"FAIL\tbench",              // the tail
	} {
		if !strings.Contains(out, want) {
			t.Errorf("elided output lost %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "lines elided") {
		t.Errorf("elided output should say what it dropped:\n%s", out)
	}
}

// TestElideDeclinesAUniformDump: head-and-tail on a file dump keeps a lot of
// bytes and almost no information, so the model should get it instead.
func TestElideDeclinesAUniformDump(t *testing.T) {
	dump := strings.Repeat("this is a long line of output\n", 400)
	if _, ok := elide(dump, defaultCompactThreshold); ok {
		t.Fatal("a uniform dump has no structure to exploit; a model summarises it far better")
	}
}

// TestElideDeclinesShortOutput: nothing to gain, and a head/tail split of
// thirty lines just loses the middle for no reason.
func TestElideDeclinesShortOutput(t *testing.T) {
	if _, ok := elide("FAIL\n"+strings.Repeat("line\n", 20), 6000); ok {
		t.Fatal("short output should be left alone")
	}
}

// TestCompactionOfATestLogCostsNoModelCall is the latency this removes: the
// compactor model must not be reached at all for the common case.
func TestCompactionOfATestLogCostsNoModelCall(t *testing.T) {
	log := failingTestLog()
	lead := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("1", "bash", map[string]any{"command": "cat log.txt"}),
		}},
		{StopReason: provider.StopEndTurn, Text: "done"},
	}}
	comp := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopEndTurn, Text: "SUMMARY", Usage: provider.Usage{OutputTokens: 5}},
	}}

	_, dir := ws(t, map[string]string{"log.txt": log})
	ag, err := New(Config{Name: "t", Model: "lead", Workdir: dir}, lead,
		WithCompactor(comp, "compactor-model", 0))
	if err != nil {
		t.Fatal(err)
	}
	res, err := ag.Run(context.Background(), "run the tests")
	if err != nil {
		t.Fatal(err)
	}
	if _, used := res.ByModel["compactor-model"]; used {
		t.Fatal("a failing test log should be trimmed without asking a model")
	}
	if !strings.Contains(lead.last.seen(), "--- FAIL: TestApply") {
		t.Fatalf("the failure did not survive compaction:\n%s", truncate(lead.last.seen(), 800))
	}
}

// TestAftercareRunsBehindTheAnswer: the review is a whole model round trip, and
// nobody is waiting for it — Run must return before it finishes.
func TestAftercareRunsBehindTheAnswer(t *testing.T) {
	release := make(chan struct{})
	rev := &blockingProvider{gate: release, text: "LGTM"}

	_, dir := ws(t, map[string]string{})
	lead := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("1", "write", map[string]any{"path": "a.go", "content": "package a\n"}),
		}},
		{StopReason: provider.StopEndTurn, Text: "written"},
	}}
	ag, err := New(Config{Name: "t", Model: "lead", Workdir: dir}, lead,
		WithReviewer(rev, "reviewer-model"))
	if err != nil {
		t.Fatal(err)
	}

	res, err := ag.Run(context.Background(), "write a.go")
	if err != nil {
		t.Fatal(err)
	}
	// Run has returned while the reviewer is still blocked, which is the point.
	if res.Text != "written" {
		t.Fatalf("text = %q", res.Text)
	}
	close(release)
	ag.Wait()
	if res.Review != "LGTM" {
		t.Fatalf("Review = %q after Wait", res.Review)
	}
}

// blockingProvider answers only once its gate is closed.
type blockingProvider struct {
	gate <-chan struct{}
	text string
}

func (p *blockingProvider) Name() string { return "blocking" }
func (p *blockingProvider) NewSession(provider.SessionConfig) provider.Session {
	return &blockingSession{p: p}
}

type blockingSession struct{ p *blockingProvider }

func (s *blockingSession) Send(ctx context.Context, _ provider.Input) (*provider.Response, error) {
	select {
	case <-s.p.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &provider.Response{StopReason: provider.StopEndTurn, Text: s.p.text}, nil
}
