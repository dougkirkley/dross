package relay

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/dougkirkley/holocron/internal/agent"
	"github.com/dougkirkley/holocron/internal/provider"
)

// DeviceOptions configures a device relay: this computer dials OUT to a holocron
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
	c, _, err := websocket.Dial(ctx, url, dopts)
	if err != nil {
		return fmt.Errorf("dial %s: %w", url, err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	c.SetReadLimit(4 << 20)

	d := &device{opts: opts, c: c, runs: map[string]context.CancelFunc{}}
	if err := d.send(ctx, Frame{Type: "hello", Device: opts.Name}); err != nil {
		return err
	}
	d.startSession(ctx, "ses_"+opts.Name)
	opts.log(fmt.Sprintf("linked %q to %s", opts.Name, opts.Server))

	for {
		var f Frame
		if err := wsjson.Read(ctx, c, &f); err != nil {
			return nil // connection closed
		}
		switch f.Type {
		case "prompt":
			d.run(ctx, f.SessionID, f.Text)
		case "interrupt":
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
	d.send(ctx, Frame{Type: "event", SessionID: id, Kind: "prompt", Data: text})

	aopts := []agent.Option{agent.WithEventHook(func(e agent.Event) {
		d.send(ctx, Frame{Type: "event", SessionID: id, Kind: e.Kind, Data: e.Data})
	})}
	if d.opts.Worker != nil && d.opts.WorkerModel != "" {
		aopts = append(aopts, agent.WithWorker(d.opts.Worker, d.opts.WorkerModel))
	}
	ag, err := agent.New(agent.Config{Name: "holocron", Model: d.opts.LeadModel, Workdir: d.opts.Workdir}, d.opts.Lead, aopts...)
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
		d.send(ctx, Frame{Type: "event", SessionID: id, Kind: "done",
			Data: fmt.Sprintf("$%.4f · %d turns", res.Cost(), res.Turns)})
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
