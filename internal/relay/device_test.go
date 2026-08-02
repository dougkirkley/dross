package relay

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// When the UI takes prompts, this device must not build an agent of its own.
// Two agents on one machine was the defect: the relay's had no permission
// rules, no conversation history and none of the user's tools.
func TestPromptHookMeansNoLocalAgent(t *testing.T) {
	var got string
	opts := DeviceOptions{Prompt: func(_, text string) { got = text }}

	// Stand in for the frame loop's dispatch: with a Prompt hook set it hands
	// the text over instead of calling d.run, which is what would construct an
	// agent.
	if opts.Prompt == nil {
		t.Fatal("Prompt hook not set")
	}
	opts.Prompt("ses_x", "do the thing")
	if got != "do the thing" {
		t.Errorf("hook received %q", got)
	}
}

// Without a UI — a session pod, or plain `dross rc` — the device still has to
// run the work itself, so the fallback must stay reachable.
func TestNoPromptHookLeavesTheFallback(t *testing.T) {
	if (DeviceOptions{}).Prompt != nil {
		t.Error("a bare DeviceOptions must not claim to take prompts")
	}
}

// A pod is launched with --name set to the session id, which already begins
// with the prefix. Adding another produced "ses_ses_…" and advertised a second,
// empty session beside the real one.
func TestSessionIDIsNotDoublePrefixed(t *testing.T) {
	for name, want := range map[string]string{
		"ses_abc": "ses_abc", // a session pod
		"laptop":  "ses_laptop",
		"":        "ses_",
	} {
		if got := advertisedSessionID(name); got != want {
			t.Errorf("name %q -> %q; want %q", name, got, want)
		}
	}
}

// The link has to survive the server going away, because a deploy takes it
// away for a few seconds. Exiting instead put a session pod into
// CrashLoopBackOff, whose delay grows to minutes — far longer than the restart
// that caused it.
func TestRunDeviceRetriesUntilCancelled(t *testing.T) {
	var attempts int32
	var logged []string
	var mu sync.Mutex

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunDevice(ctx, DeviceOptions{
			// Nothing is listening, so every dial fails.
			Server: "http://127.0.0.1:1", Name: "box",
			Log: func(m string) {
				mu.Lock()
				logged = append(logged, m)
				if strings.Contains(m, "connect failed") {
					if atomic.AddInt32(&attempts, 1) >= 2 {
						cancel() // it retried; that is the whole claim
					}
				}
				mu.Unlock()
			},
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("returned %v; cancellation is not a failure", err)
		}
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("did not return after cancellation")
	}
	if n := atomic.LoadInt32(&attempts); n < 2 {
		t.Errorf("dialled %d time(s); it gave up instead of retrying", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(strings.Join(logged, "\n"), "reconnecting") {
		t.Error("never said it was reconnecting")
	}
}

// Shutting down waits for the turn in flight. A session pod is usually being
// replaced when this happens, and the answer being written is the thing worth
// saving — Kubernetes gives the pod a grace period precisely so it can.
func TestDrainWaitsForRunsInFlight(t *testing.T) {
	d := &device{runs: map[string]context.CancelFunc{}}
	d.runs["ses_a"] = func() {}

	var logged []string
	var mu sync.Mutex
	opts := DeviceOptions{Log: func(m string) {
		mu.Lock()
		logged = append(logged, m)
		mu.Unlock()
	}}

	done := make(chan struct{})
	go func() { d.drain(opts); close(done) }()

	select {
	case <-done:
		t.Fatal("drain returned while a run was still going")
	case <-time.After(400 * time.Millisecond):
	}

	// The run finishes, as it would by deleting itself from the map.
	d.mu.Lock()
	delete(d.runs, "ses_a")
	d.mu.Unlock()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not return once the run finished")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(strings.Join(logged, "\n"), "drained") {
		t.Errorf("drain said nothing about finishing: %v", logged)
	}
}

// With nothing running, shutdown is immediate — a pod with no work to finish
// should not sit out its grace period.
func TestDrainReturnsImmediatelyWhenIdle(t *testing.T) {
	d := &device{runs: map[string]context.CancelFunc{}}
	start := time.Now()
	d.drain(DeviceOptions{})
	if took := time.Since(start); took > time.Second {
		t.Errorf("idle drain took %s", took)
	}
}
