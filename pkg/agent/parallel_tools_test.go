package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dougkirkley/dross/pkg/provider"
)

// TestReadOnlyToolsRunConcurrently proves a turn's read-only calls overlap
// rather than queue.
//
// The server refuses to answer any request until all three have arrived, so a
// serial loop can never get past the first one: the test fails on a timeout
// instead of on a stopwatch, which is the only way to assert concurrency
// without a flaky threshold.
func TestReadOnlyToolsRunConcurrently(t *testing.T) {
	const n = 3
	var mu sync.Mutex
	arrived := 0
	all := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrived++
		if arrived == n {
			close(all)
		}
		mu.Unlock()
		select {
		case <-all:
		case <-time.After(5 * time.Second):
			http.Error(w, "timed out waiting for concurrent peers", http.StatusGatewayTimeout)
			return
		}
		fmt.Fprintf(w, "page %s", strings.TrimPrefix(r.URL.Path, "/"))
	}))
	defer srv.Close()

	calls := make([]provider.ToolCall, n)
	for i := range calls {
		calls[i] = toolCall(fmt.Sprint(i), "web_fetch", map[string]any{
			"url": fmt.Sprintf("%s/%d", srv.URL, i),
		})
	}
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: calls},
		{StopReason: provider.StopEndTurn, Text: "read them"},
	}}

	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, prov)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := ag.Run(ctx, "fetch all three"); err != nil {
		t.Fatal(err)
	}

	// Every page came back, and each landed against its own call — order is what
	// pairs a result with the tool_use id the model is waiting on.
	last := prov.last.lastInput
	for i := 0; i < n; i++ {
		if want := fmt.Sprintf("page %d", i); !strings.Contains(last, want) {
			t.Fatalf("missing %q in tool results: %q", want, last)
		}
	}
}

// TestBatchKeepsOrderAroundWrites is the safety half: read-only calls are only
// grouped with their neighbours, so a read that follows an edit in the same
// message still sees the edited file.
func TestBatchKeepsOrderAroundWrites(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("1", "read", map[string]any{"path": "f.txt"}),
			toolCall("2", "edit", map[string]any{"path": "f.txt", "old_str": "before", "new_str": "after"}),
			toolCall("3", "read", map[string]any{"path": "f.txt"}),
		}},
		{StopReason: provider.StopEndTurn, Text: "done"},
	}}

	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), "read, edit, read"); err != nil {
		t.Fatal(err)
	}

	last := prov.last.lastInput
	if !strings.Contains(last, "before") {
		t.Fatalf("first read should predate the edit: %q", last)
	}
	if !strings.Contains(last, "after") {
		t.Fatalf("second read should see the edit: %q", last)
	}
}

// TestParallelSafeExcludesSideEffects pins the closed list: anything that can
// write, run a command, ask the user, or be someone else's code stays serial.
func TestParallelSafeExcludesSideEffects(t *testing.T) {
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, &scriptedProvider{},
		WithTool(provider.ToolDef{Name: "custom"}, func(context.Context, json.RawMessage) (string, bool) { return "", false }),
		WithPluginTools([]PluginSpec{{Name: "plug", Command: []string{"true"}}}),
	)
	if err != nil {
		t.Fatal(err)
	}
	safe := []string{"read", "grep", "glob", "web_search", "web_fetch"}
	unsafe := []string{"write", "edit", "bash", "monitor", "shell_output", "task", "ask_user", "publish", "skill", "custom", "plug"}
	for _, n := range safe {
		if !ag.parallelSafe(provider.ToolCall{Name: n}) {
			t.Errorf("%s should run in parallel", n)
		}
	}
	for _, n := range unsafe {
		if ag.parallelSafe(provider.ToolCall{Name: n}) {
			t.Errorf("%s must not run in parallel", n)
		}
	}
}

// slowProvider answers after a delay but reports a much smaller model latency —
// the shape of a backend that shells out to a CLI, where most of the wait is
// starting a process rather than thinking.
type slowProvider struct {
	wait time.Duration
	api  time.Duration
}

func (p *slowProvider) Name() string { return "slow" }
func (p *slowProvider) NewSession(provider.SessionConfig) provider.Session {
	return &slowSession{p: p}
}

type slowSession struct {
	p    *slowProvider
	sent int
}

func (s *slowSession) Send(context.Context, provider.Input) (*provider.Response, error) {
	time.Sleep(s.p.wait)
	s.sent++
	return &provider.Response{StopReason: provider.StopEndTurn, Text: "done", APITime: s.p.api}, nil
}

// TestProviderOverheadIsChargedToTheAgent: when a backend says how long the
// model took, the rest of the wait is the provider's own price and has to show
// up as such — otherwise a per-turn process start reads as a slow model and
// never gets fixed.
func TestProviderOverheadIsChargedToTheAgent(t *testing.T) {
	prov := &slowProvider{wait: 300 * time.Millisecond, api: 50 * time.Millisecond}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, prov)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := ag.Run(context.Background(), "hi")
	wall := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if res.ModelTime != prov.api {
		t.Fatalf("model time = %v, want the reported %v", res.ModelTime, prov.api)
	}
	if overhead := wall - res.ModelTime - res.ToolTime; overhead < 200*time.Millisecond {
		t.Fatalf("overhead = %v, want the provider's own wait to land here", overhead)
	}
}

// TestRunReportsTimeSplit checks the wall-clock accounting the benchmark reads:
// time blocked on the model and time running tools are counted separately, and
// neither exceeds the run.
func TestRunReportsTimeSplit(t *testing.T) {
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("1", "bash", map[string]any{"command": "sleep 0.2"}),
		}},
		{StopReason: provider.StopEndTurn, Text: "done"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, prov)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := ag.Run(context.Background(), "sleep")
	wall := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if res.ToolTime < 150*time.Millisecond {
		t.Fatalf("tool time = %v, want the sleep to show up", res.ToolTime)
	}
	if res.ModelTime+res.ToolTime > wall {
		t.Fatalf("model %v + tool %v exceeds wall %v", res.ModelTime, res.ToolTime, wall)
	}
}
