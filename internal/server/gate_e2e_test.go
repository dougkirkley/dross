package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dougkirkley/dross/internal/relay"
	"github.com/dougkirkley/dross/internal/storage"
	"github.com/dougkirkley/dross/pkg/agent"
)

// The whole gated-command loop, over a real websocket and real HTTP:
//
//	phone → prompt → server → device
//	device hits a gate → ask → server → phone
//	phone answers → server → device → the run continues
//
// Every piece of this was unit-tested in isolation; this is the one that fails
// if the ends do not agree on the wire.
func TestGatedCommandFromRemoteIsAnsweredRemotely(t *testing.T) {
	store := storage.NewSessionStore(t.TempDir())
	mgr := NewManager(&fakeProvider{}, nil, "m", t.TempDir())
	srv := New(store, mgr, nil, testArts(t)) // no auth: identity is empty everywhere
	hs := httptest.NewServer(srv)
	defer hs.Close()

	// The linked machine. Prompt is set, which is what tells the relay that a
	// UI owns the agent — so this stands in for the TUI, and the relay builds
	// nothing of its own.
	gotAnswer := make(chan string, 1)
	linked := make(chan struct{})
	var askFn func(string) (string, error)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = relay.RunDevice(ctx, relay.DeviceOptions{
			Server: hs.URL, Name: "laptop", Workdir: t.TempDir(),
			Log:    func(m string) { t.Log("device:", m) },
			Lead: &fakeProvider{}, LeadModel: "m",
			Ready: func(_ func(kind, data string), ask func(string) (string, error)) {
				askFn = ask
				close(linked)
			},
			Prompt: func(_, _ string) {
				// What the TUI does when a gate trips mid-run: put the question
				// to whoever asked for the work and wait.
				req := agent.AskRequest{Questions: []agent.AskQuestion{{
					Header:   "bash",
					Question: "Allow this bash?\nrm -rf build/",
					Options:  []string{"Allow", "Deny"},
				}}}
				payload, _ := json.Marshal(req)
				ans, err := askFn(string(payload))
				if err != nil {
					gotAnswer <- "error: " + err.Error()
					return
				}
				gotAnswer <- ans
			},
		})
	}()

	select {
	case <-linked:
	case <-time.After(10 * time.Second):
		t.Fatal("device never linked")
	}

	// The device advertises ses_<name>; wait for the server to mirror it.
	const sid = "ses_laptop"
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, ok := mgr.getAny(sid); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session never appeared on the server")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Phone sends the prompt.
	post(t, hs.URL+"/api/sessions/"+sid+"/prompt", `{"prompt":"clean the build dir"}`, http.StatusAccepted)

	// The gate should surface as an ordinary event on the session.
	askID, question := waitForAsk(t, hs.URL, sid)
	if !strings.Contains(question, "rm -rf build/") {
		t.Errorf("ask did not carry the command: %s", question)
	}

	// Phone answers.
	post(t, hs.URL+"/api/sessions/"+sid+"/answer",
		`{"ask_id":"`+askID+`","answer":"Allow"}`, http.StatusOK)

	select {
	case ans := <-gotAnswer:
		if ans != "Allow" {
			t.Fatalf("the run resumed with %q; want Allow", ans)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the answer never reached the blocked run")
	}
}

// An answer nobody is waiting for must be refused rather than banked for the
// next question.
func TestAnswerWithNoPendingGateIsRejected(t *testing.T) {
	store := storage.NewSessionStore(t.TempDir())
	mgr := NewManager(&fakeProvider{}, nil, "m", t.TempDir())
	srv := New(store, mgr, nil, testArts(t))
	hs := httptest.NewServer(srv)
	defer hs.Close()

	mgr.upsertDeviceSession("laptop", relaySession("ses_laptop"))
	post(t, hs.URL+"/api/sessions/ses_laptop/answer", `{"answer":"Allow"}`, http.StatusConflict)
}

func post(t *testing.T, url, body string, want int) {
	t.Helper()
	r, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer r.Body.Close()
	if r.StatusCode != want {
		t.Fatalf("POST %s = %d; want %d", url, r.StatusCode, want)
	}
}

// waitForAsk polls the session's events for the gate, returning its id and the
// question payload.
func waitForAsk(t *testing.T, base, sid string) (string, string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r, err := http.Get(base + "/api/sessions/" + sid + "/events")
		if err != nil {
			t.Fatalf("events: %v", err)
		}
		var body struct {
			Events []struct{ Kind, Data string } `json:"events"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		r.Body.Close()
		for _, e := range body.Events {
			if e.Kind == "ask" {
				id, payload, _ := strings.Cut(e.Data, "\x1f")
				return id, payload
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no ask event reached the session")
	return "", ""
}

// Denying has to reach the run too, and reach it as a refusal — a gate that
// only ever transmits approval is not a gate.
func TestDeniedGateReachesTheRun(t *testing.T) {
	store := storage.NewSessionStore(t.TempDir())
	mgr := NewManager(&fakeProvider{}, nil, "m", t.TempDir())
	srv := New(store, mgr, nil, testArts(t))
	hs := httptest.NewServer(srv)
	defer hs.Close()

	got := make(chan string, 1)
	linked := make(chan struct{})
	var askFn func(string) (string, error)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = relay.RunDevice(ctx, relay.DeviceOptions{
			Server: hs.URL, Name: "box", Workdir: t.TempDir(),
			Lead: &fakeProvider{}, LeadModel: "m",
			Ready: func(_ func(kind, data string), ask func(string) (string, error)) {
				askFn = ask
				close(linked)
			},
			Prompt: func(_, _ string) {
				req := agent.AskRequest{Questions: []agent.AskQuestion{{
					Header: "bash", Question: "Allow this bash?\nrm -rf /",
					Options: []string{"Allow", "Deny"},
				}}}
				payload, _ := json.Marshal(req)
				ans, err := askFn(string(payload))
				if err != nil {
					got <- "error"
					return
				}
				got <- ans
			},
		})
	}()

	<-linked
	const sid = "ses_box"
	for i := 0; i < 500; i++ {
		if _, ok := mgr.getAny(sid); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	post(t, hs.URL+"/api/sessions/"+sid+"/prompt", `{"prompt":"wipe it"}`, http.StatusAccepted)
	askID, _ := waitForAsk(t, hs.URL, sid)
	post(t, hs.URL+"/api/sessions/"+sid+"/answer",
		`{"ask_id":"`+askID+`","answer":"Deny"}`, http.StatusOK)

	select {
	case ans := <-got:
		if ans != "Deny" {
			t.Fatalf("the run saw %q; want Deny", ans)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the denial never reached the run")
	}
}
