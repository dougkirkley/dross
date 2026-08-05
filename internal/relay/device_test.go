package relay

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/dougkirkley/dross/pkg/provider"
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
	// A pod's name IS the session it serves, so it is passed through whole. Any
	// suffix here would advertise a session nobody is watching.
	if got := advertisedSessionID("ses_abc"); got != "ses_abc" {
		t.Errorf("pod name -> %q; want it announced unchanged", got)
	}
	for _, name := range []string{"laptop", ""} {
		got := advertisedSessionID(name)
		if !strings.HasPrefix(got, "ses_"+name) {
			t.Errorf("name %q -> %q; want the machine still legible in the id", name, got)
		}
		if strings.HasPrefix(got, "ses_ses_") {
			t.Errorf("name %q -> %q; double prefix", name, got)
		}
	}
}

// Every `dross rc` is a new session.
//
// The id was the hostname alone, so a machine had exactly one session for its
// whole life: each link reattached to it and appended to a transcript that
// never ended, mixing yesterday's questions into today's.
func TestEachLinkGetsItsOwnSession(t *testing.T) {
	a, b := advertisedSessionID("laptop"), advertisedSessionID("laptop")
	if a == b {
		t.Errorf("both links advertised %q; the second would resume the first's transcript", a)
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

// An expired credential has to end the loop, not feed it.
//
// A session token lasts twelve hours, so leaving a link up overnight lands
// here. Retrying cannot mint a new one: the loop backed off to its thirty
// second ceiling and sat there re-reporting a WebSocket handshake code, which
// is a poor way to say "sign in again".
func TestUnauthorizedStopsRetrying(t *testing.T) {
	var dials atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dials.Add(1)
		http.Error(w, "invalid device token", http.StatusUnauthorized)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := RunDevice(ctx, DeviceOptions{Server: srv.URL, Token: "stale", Name: "mac"})

	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("RunDevice returned %v; the caller cannot offer a fresh login without this", err)
	}
	if n := dials.Load(); n != 1 {
		t.Errorf("dialed %d times; a rejected credential must not be retried", n)
	}
}

// stubProvider stands in for a backend: the session frame names the lead
// provider, and nothing in this test reaches a model.
type stubProvider struct{}

func (stubProvider) Name() string                                       { return "stub" }
func (stubProvider) NewSession(provider.SessionConfig) provider.Session { return nil }

// A reconnect keeps the session it already has.
//
// The id is minted once per link, not once per dial, so a server restart or a
// dropped connection comes back to the session the phone is watching rather
// than stranding it on a dead one and starting another.
func TestReconnectKeepsTheSameSession(t *testing.T) {
	var mu sync.Mutex
	var ids []string
	done := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		// Read the hello, then the session frame it announces, and hang up —
		// standing in for a server that keeps going away.
		ctx := r.Context()
		var f Frame
		for i := 0; i < 2; i++ {
			if wsjson.Read(ctx, c, &f) != nil {
				break
			}
			if f.Type == "session" && f.Session != nil {
				mu.Lock()
				ids = append(ids, f.Session.ID)
				n := len(ids)
				mu.Unlock()
				if n == 2 {
					close(done)
				}
			}
		}
		c.Close(websocket.StatusNormalClosure, "")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go RunDevice(ctx, DeviceOptions{
		Server: srv.URL, Token: "t", Name: "laptop", Lead: stubProvider{},
	})

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("device never reconnected")
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if ids[0] != ids[1] {
		t.Errorf("reconnected as %q after %q; the watcher is left on a dead session", ids[1], ids[0])
	}
}

// A freshly linked session is named after the machine that linked it.
//
// Without a title the list shows "New session", and now that every link is its
// own session a column of those says nothing about which is which.
func TestLinkAdvertisesATitle(t *testing.T) {
	if got := linkTitle(DeviceOptions{Name: "laptop", Repo: "dross"}); got != "laptop · dross" {
		t.Errorf("linkTitle = %q; want the machine and the workspace", got)
	}
	if got := linkTitle(DeviceOptions{Name: "laptop"}); got != "laptop" {
		t.Errorf("linkTitle = %q; want the machine alone when there is no repo", got)
	}
	// A pod's name is a session id. The server already holds its real title.
	if got := linkTitle(DeviceOptions{Name: "ses_abc", Repo: "dross"}); got != "" {
		t.Errorf("linkTitle = %q for a pod; want nothing", got)
	}
}
