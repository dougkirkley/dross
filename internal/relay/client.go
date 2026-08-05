package relay

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/dougkirkley/dross/pkg/agent"
	"github.com/dougkirkley/dross/pkg/provider"
)

// DeviceOptions configures a device relay: this computer dials OUT to a dross
// server and advertises a session the phone can drive. Prompts are relayed back
// and run locally; the agent's events stream to the server.
type DeviceOptions struct {
	Server, Token       string // server URL and the SSO session token to auth as
	Name, Workdir, Repo string
	// Branch the workspace is checked out on, when it was made for this
	// session. Told to the model, which would otherwise have to go and look.
	Branch      string
	Lead        provider.Provider
	LeadModel   string
	Worker      provider.Provider
	WorkerModel string
	Log         func(string) // optional status sink

	// Ready is called once the link is up, with a function that publishes a
	// locally-driven event into the same session the phone is watching.
	//
	// The relay was one-way: the phone could drive this machine, but work
	// started at the keyboard went nowhere, so the two ends showed different
	// halves of the same session. Publishing local events closes the loop —
	// whoever is holding the phone sees what you type, and you see what they
	// send.
	Ready func(publish func(kind, data string), ask func(payload string) (string, error))

	// Prompt and Interrupt, when set, hand the remote's requests to the local
	// UI instead of running them here.
	//
	// This device can build an agent of its own, and does when nothing else
	// will (a session pod, or plain `dross rc`). But when a TUI is attached it
	// already owns one: a conversation that persists across turns, with the
	// permission mode, identity, turn limit, MCP servers and plugins the user
	// configured. Running a second agent beside it would answer the phone with
	// a different, ungated assistant that remembers nothing — so when the UI
	// offers to take prompts, it takes all of them.
	Prompt    func(sessionID, text string)
	Interrupt func(sessionID string)

	// Policy is the user's tool and permission policy, applied when this device
	// does build an agent of its own. Without it a headless worker ran with no
	// rules at all, so a gate the user configured simply did not exist here.
	Policy []agent.Option
}

func (o DeviceOptions) log(msg string) {
	if o.Log != nil {
		o.Log(msg)
	}
}

// RunDevice links this computer to the server and serves phone-driven prompts
// until the context is cancelled or the connection drops.
// RunDevice keeps this machine linked until the context is cancelled,
// reconnecting when the link drops.
//
// It retries rather than returning, because the server restarting is ordinary:
// a deploy takes it away for a few seconds and every linked session would
// otherwise exit. In a pod that means CrashLoopBackOff, whose delay grows to
// minutes, so a ten-second restart left sessions dead for far longer than the
// thing that broke them.
func RunDevice(ctx context.Context, opts DeviceOptions) error {
	const (
		firstWait = time.Second
		maxWait   = 30 * time.Second
	)
	wait := firstWait
	// Once per link, not once per dial: a reconnect has to land back on the
	// session the phone is already watching.
	sessionID := advertisedSessionID(opts.Name)
	// Said once, on the first link rather than on every one. The server
	// restarting is ordinary and reconnecting is not a new session — a line
	// about what was read repeated down the transcript every time the deploy
	// bounced would be worse than not saying it at all.
	said := false
	for {
		err := runDeviceOnce(ctx, opts, sessionID, &said)
		if ctx.Err() != nil {
			return nil // asked to stop
		}
		if errors.Is(err, ErrUnauthorized) {
			return err // a retry cannot mint a credential; hand it back
		}
		if err != nil {
			opts.log(fmt.Sprintf("reconnecting in %s…", wait))
		} else {
			// A clean drop is still a drop; come back promptly.
			wait = firstWait
			opts.log("link closed; reconnecting…")
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil
		}
		if wait *= 2; wait > maxWait {
			wait = maxWait
		}
	}
}

// ErrUnauthorized is returned when the server rejects the credential. It is
// separated from every other dial failure because it is the one a retry cannot
// fix: a session token lasts twelve hours, and once it lapses the loop below
// will back off to thirty seconds and stay there, reporting "expected handshake
// response status code 101 but got 401" every half minute until someone reads
// it closely enough to recognise an expired login.
var ErrUnauthorized = errors.New("server rejected the credential")

func runDeviceOnce(ctx context.Context, opts DeviceOptions, sessionID string, said *bool) error {
	url := strings.TrimRight(opts.Server, "/") + "/api/device/connect"
	url = strings.Replace(strings.Replace(url, "https://", "wss://", 1), "http://", "ws://", 1)
	dopts := &websocket.DialOptions{
		HTTPHeader: map[string][]string{"Authorization": {"Bearer " + opts.Token}},
	}
	// Announced before the dial, not after. Everything up to here can hang or
	// fail — a wrong scheme against a plaintext port, an unresolvable service,
	// a rejected credential — and without this line the process sits silent
	// with nothing to say which of those it is. That is exactly how a session
	// pod looks when it never attaches: running, and quiet.
	opts.log(fmt.Sprintf("connecting to %s as %q…", url, opts.Name))
	// Bounded, because a refused connection reports itself but a dropped one
	// does not: a network policy that filters the packets leaves the dial
	// waiting on a reply that is never coming. Failing after a minute gives an
	// error to log and, in a pod, a restart that retries.
	dialCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	c, resp, err := websocket.Dial(dialCtx, url, dopts)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			opts.log("the server rejected this login (401)")
			return ErrUnauthorized
		}
		opts.log(fmt.Sprintf("connect failed: %v", err))
		return fmt.Errorf("dial %s: %w", url, err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	c.SetReadLimit(4 << 20)

	d := &device{opts: opts, c: c, runs: map[string]context.CancelFunc{}, asks: map[string]chan string{}}
	if err := d.send(ctx, Frame{Type: "hello", Device: opts.Name}); err != nil {
		return err
	}
	d.startSession(ctx, sessionID)
	// The session id is worth saying now that it is not the device name: a
	// machine linked twice has two sessions in the list, and this is what tells
	// them apart.
	opts.log(fmt.Sprintf("linked %q to %s as %s", opts.Name, opts.Server, sessionID))
	// What this session was given about the workspace it starts in. Only this
	// side can say: the server named the repository and the branch, but the
	// files are here, and a session that has quietly read a project's own
	// documents looks identical to one that has not.
	if said != nil && !*said {
		// Its own kind, not "meta": the server keeps one of these per session
		// and drops a repeat, which is what makes this true once rather than
		// once per link. Guarding here as well only saves the round trip.
		if line := agent.WorkspaceContext(opts.Workdir); line != "" {
			d.publishLocal(ctx, sessionID, WorkspaceKind, line)
			*said = true
		}
	}
	if opts.Ready != nil {
		opts.Ready(
			func(kind, data string) { d.publishLocal(ctx, sessionID, kind, data) },
			func(payload string) (string, error) { return d.askRemote(ctx, sessionID, payload) },
		)
	}

	// Prompts are handed to a worker rather than run on the read loop.
	//
	// The loop has to stay free to read: a run blocked on a permission gate is
	// waiting for an answer frame that arrives on this very connection, so
	// dispatching the prompt inline deadlocks the thing it is waiting for. The
	// same goes for an interrupt sent mid-run. One worker rather than a
	// goroutine each, so prompts still arrive in the order they were sent.
	prompts := make(chan Frame, 64)
	defer close(prompts)
	go func() {
		for f := range prompts {
			if opts.Prompt != nil {
				opts.Prompt(f.SessionID, f.Text)
				continue
			}
			d.run(ctx, f.SessionID, f.Text)
		}
	}()

	for {
		var f Frame
		if err := wsjson.Read(ctx, c, &f); err != nil {
			if ctx.Err() != nil {
				// Being shut down, not dropped. Finish whatever is running
				// before letting the process go: the pod is usually being
				// replaced, and the answer in flight is the thing worth saving.
				d.drain(opts)
				return nil
			}
			// Say so: an unexplained exit reads as a crash, and the reason for
			// the drop is the useful part.
			opts.log(fmt.Sprintf("disconnected from %s: %v", opts.Server, err))
			return nil
		}
		switch f.Type {
		case "prompt":
			// The UI owns the agent when it took the hook; it echoes and
			// publishes the run itself.
			select {
			case prompts <- f:
			case <-ctx.Done():
				return nil
			}
		case "answer":
			d.deliverAnswer(f.AskID, f.Text)
		case "interrupt":
			if opts.Interrupt != nil {
				opts.Interrupt(f.SessionID)
				break
			}
			d.interrupt(f.SessionID)
		}
	}
}

// device is the linked-computer side of the relay.
type device struct {
	opts   DeviceOptions
	c      *websocket.Conn
	sendMu sync.Mutex
	mu     sync.Mutex
	runs   map[string]context.CancelFunc // sessionID -> cancel

	askMu  sync.Mutex
	asks   map[string]chan string // askID -> waiting run
	askSeq int
}

func (d *device) send(ctx context.Context, f Frame) error {
	d.sendMu.Lock()
	defer d.sendMu.Unlock()
	return wsjson.Write(ctx, d.c, f)
}

// startSession advertises an idle session to the server.
func (d *device) startSession(ctx context.Context, id string) {
	_ = d.send(ctx, Frame{Type: "session", Session: &SessionInfo{
		ID: id, Model: d.opts.LeadModel, Provider: d.opts.Lead.Name(),
		Workspace: d.opts.Repo, Status: "idle", Title: linkTitle(d.opts),
	}})
}

// linkTitle names a session after the machine that just linked it.
//
// A link advertised no title at all, so until the first prompt arrived to
// supply one the session was listed as "New session" — and with every link now
// getting its own session, a list of those says nothing about which machine any
// of them is. The workspace comes too, since one machine linked from two
// checkouts is the case where the hostname alone stops being an answer.
//
// Empty for a session pod. Its name is the session id, which is no kind of
// title, and the server already holds the one the user's prompt gave it.
func linkTitle(opts DeviceOptions) string {
	if strings.HasPrefix(opts.Name, "ses_") {
		return ""
	}
	if opts.Repo != "" {
		return opts.Name + " · " + opts.Repo
	}
	return opts.Name
}

// run executes a prompt in a session locally and streams its events back.
func (d *device) run(ctx context.Context, id, text string) {
	// Detached from the connection's context on purpose. Shutting down cancels
	// that context, and a run descending from it would be killed by the very
	// signal drain exists to survive — the answer abandoned half-written rather
	// than finished.
	//
	// It also means a dropped connection no longer aborts the work. That is the
	// better end of the trade: the edit lands in the workspace, which outlives
	// both the connection and the pod, where cancelling leaves it half-applied.
	// Interrupts still work — they cancel this run directly, not its parent.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	d.mu.Lock()
	d.runs[id] = cancel
	d.mu.Unlock()

	d.send(ctx, Frame{Type: "session", Session: &SessionInfo{
		ID: id, Model: d.opts.LeadModel, Provider: d.opts.Lead.Name(),
		Workspace: d.opts.Repo, Status: "running", Title: firstLine(text),
	}})
	// Note: the server already emits the "prompt" event when it relays the
	// prompt to us, so don't echo it back here (would double it in the log).

	aopts := []agent.Option{agent.WithEventHook(func(e agent.Event) {
		d.send(ctx, Frame{Type: "event", SessionID: id, Kind: e.Kind, Data: e.Data})
	})}
	if d.opts.Worker != nil && d.opts.WorkerModel != "" {
		aopts = append(aopts, agent.WithWorker(d.opts.Worker, d.opts.WorkerModel))
	}
	aopts = append(aopts, d.opts.Policy...)
	// Repo and Branch are what a session pod knows about itself and the model
	// otherwise has to go and find out.
	ag, err := agent.New(agent.Config{
		Name: "dross", Model: d.opts.LeadModel, Workdir: d.opts.Workdir,
		Repo: d.opts.Repo, Branch: d.opts.Branch,
	}, d.opts.Lead, aopts...)
	if err != nil {
		d.send(ctx, Frame{Type: "event", SessionID: id, Kind: "error", Data: err.Error()})
		return
	}
	go func() {
		defer func() {
			d.mu.Lock()
			delete(d.runs, id)
			d.mu.Unlock()
		}()
		res, err := ag.Run(runCtx, text)
		if err != nil {
			d.send(ctx, Frame{Type: "event", SessionID: id, Kind: "error", Data: err.Error()})
			return
		}
		done := fmt.Sprintf("$%.4f · %d turns", res.Cost(), res.Turns)
		d.send(ctx, Frame{Type: "event", SessionID: id, Kind: "done", Data: done})
	}()
}

func (d *device) interrupt(id string) {
	d.mu.Lock()
	if c := d.runs[id]; c != nil {
		c()
	}
	d.mu.Unlock()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return strings.TrimSpace(s)
}

// publishLocal streams an event produced by work started at this machine into
// the session the server is mirroring, and keeps that session's status honest
// so the phone shows it running rather than idle.
//
// Status is derived here rather than asked of the caller: the local UI should
// not have to know the relay's session vocabulary to stay in sync.
func (d *device) publishLocal(ctx context.Context, id, kind, data string) {
	switch kind {
	case "prompt":
		d.send(ctx, Frame{Type: "session", Session: &SessionInfo{
			ID: id, Model: d.opts.LeadModel, Provider: d.opts.Lead.Name(),
			Workspace: d.opts.Repo, Status: "running", Title: firstLine(data),
		}})
	case "done", "error":
		defer d.send(ctx, Frame{Type: "session", Session: &SessionInfo{
			ID: id, Model: d.opts.LeadModel, Provider: d.opts.Lead.Name(),
			Workspace: d.opts.Repo, Status: "idle",
		}})
	}
	d.send(ctx, Frame{Type: "event", SessionID: id, Kind: kind, Data: data})
}

// askRemote puts a question to whoever is driving this session and waits for
// their reply.
//
// The run is blocked while this is outstanding, which is the point: a gate that
// returned early would be a gate that did not gate. It ends on an answer, on
// the link dropping, or on the run being cancelled — the caller treats an error
// as a refusal, so every one of those fails closed.
func (d *device) askRemote(ctx context.Context, sessionID, payload string) (string, error) {
	d.askMu.Lock()
	d.askSeq++
	id := fmt.Sprintf("%s-%d", sessionID, d.askSeq)
	reply := make(chan string, 1)
	d.asks[id] = reply
	d.askMu.Unlock()

	defer func() {
		d.askMu.Lock()
		delete(d.asks, id)
		d.askMu.Unlock()
	}()

	if err := d.send(ctx, Frame{Type: "ask", SessionID: sessionID, AskID: id, Data: payload}); err != nil {
		return "", fmt.Errorf("ask: %w", err)
	}
	select {
	case ans := <-reply:
		return ans, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// deliverAnswer wakes the run waiting on an ask. An answer to a question nobody
// is waiting for is dropped: the run was cancelled or already gave up, and
// applying it to whatever came next would answer the wrong question.
func (d *device) deliverAnswer(id, text string) {
	d.askMu.Lock()
	reply := d.asks[id]
	d.askMu.Unlock()
	if reply == nil {
		return
	}
	select {
	case reply <- text:
	default:
	}
}

// advertisedSessionID is the session a device announces itself as.
//
// A laptop links under its hostname and gets a fresh session each time it
// links; a session pod is launched with --name set to the session id it exists
// to serve, and must announce exactly that. Prepending unconditionally gave
// those pods "ses_ses_…" and advertised a second, empty session beside the real
// one.
//
// The laptop's id used to be the hostname alone, which is stable — so every
// `dross rc` reattached to the one session that machine had ever had, and its
// transcript grew without end. Reconnects still have to land back on the
// session the phone is watching, so this is called once per link, not once per
// dial: a dropped connection keeps its id, a new process gets a new one.
func advertisedSessionID(name string) string {
	if strings.HasPrefix(name, "ses_") {
		return name
	}
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Nothing sane to fall back to but the old stable id; a session that
		// resumes yesterday's transcript beats no session at all.
		return "ses_" + name
	}
	return "ses_" + name + "-" + base32Lower.EncodeToString(b[:])
}

// base32Lower keeps the suffix case-insensitive and free of the characters that
// read ambiguously in a terminal.
var base32Lower = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// drainWait bounds how long a shutting-down device waits for its work.
//
// Longer than a quick turn and shorter than the pod's grace period, so
// Kubernetes is not the thing that decides: past this the process gives up and
// exits, rather than being killed part-way through writing a file.
const drainWait = 90 * time.Second

// drain waits for in-flight runs to finish while the device is shutting down.
//
// The connection is already gone by this point, so the events these runs
// produce cannot be delivered — but the work itself lands in the workspace,
// which outlives the pod, and a half-applied edit is worse than a late one.
func (d *device) drain(opts DeviceOptions) {
	d.mu.Lock()
	n := len(d.runs)
	d.mu.Unlock()
	if n == 0 {
		return
	}
	opts.log(fmt.Sprintf("draining: waiting for %d run(s) to finish…", n))

	deadline := time.After(drainWait)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			d.mu.Lock()
			n = len(d.runs)
			d.mu.Unlock()
			if n == 0 {
				opts.log("drained")
				return
			}
		case <-deadline:
			opts.log(fmt.Sprintf("drain timed out with %d run(s) still going", n))
			return
		}
	}
}
