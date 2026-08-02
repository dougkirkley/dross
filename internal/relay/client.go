package relay

import (
	"context"
	"fmt"
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
	Lead                provider.Provider
	LeadModel           string
	Worker              provider.Provider
	WorkerModel         string
	Log                 func(string) // optional status sink

	// Ready is called once the link is up, with a function that publishes a
	// locally-driven event into the same session the phone is watching.
	//
	// The relay was one-way: the phone could drive this machine, but work
	// started at the keyboard went nowhere, so the two ends showed different
	// halves of the same session. Publishing local events closes the loop —
	// whoever is holding the phone sees what you type, and you see what they
	// send.
	Ready func(publish func(kind, data string))

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
func RunDevice(ctx context.Context, opts DeviceOptions) error {
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
	c, _, err := websocket.Dial(dialCtx, url, dopts)
	if err != nil {
		opts.log(fmt.Sprintf("connect failed: %v", err))
		return fmt.Errorf("dial %s: %w", url, err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	c.SetReadLimit(4 << 20)

	d := &device{opts: opts, c: c, runs: map[string]context.CancelFunc{}}
	if err := d.send(ctx, Frame{Type: "hello", Device: opts.Name}); err != nil {
		return err
	}
	sessionID := "ses_" + opts.Name
	d.startSession(ctx, sessionID)
	opts.log(fmt.Sprintf("linked %q to %s", opts.Name, opts.Server))
	if opts.Ready != nil {
		opts.Ready(func(kind, data string) { d.publishLocal(ctx, sessionID, kind, data) })
	}

	for {
		var f Frame
		if err := wsjson.Read(ctx, c, &f); err != nil {
			// Say so: an unexplained exit reads as a crash, and the reason for
			// the drop is the useful part.
			opts.log(fmt.Sprintf("disconnected from %s: %v", opts.Server, err))
			return nil
		}
		switch f.Type {
		case "prompt":
			if opts.Prompt != nil {
				// The UI owns the agent; it echoes and publishes the run itself.
				opts.Prompt(f.SessionID, f.Text)
				break
			}
			d.run(ctx, f.SessionID, f.Text)
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
		Workspace: d.opts.Repo, Status: "idle",
	}})
}

// run executes a prompt in a session locally and streams its events back.
func (d *device) run(ctx context.Context, id, text string) {
	runCtx, cancel := context.WithCancel(ctx)
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
	ag, err := agent.New(agent.Config{Name: "dross", Model: d.opts.LeadModel, Workdir: d.opts.Workdir}, d.opts.Lead, aopts...)
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
