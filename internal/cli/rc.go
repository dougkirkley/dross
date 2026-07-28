package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/spf13/cobra"

	"github.com/dougkirkley/holocron/internal/agent"
	"github.com/dougkirkley/holocron/internal/provider"
	"github.com/dougkirkley/holocron/internal/relay"
)

// newRCCmd is `holocron rc`: link this computer to a holocron server as a
// device, so the phone app can drive sessions that run here. The laptop dials
// OUT over a WebSocket (the phone never needs to reach it directly); the server
// relays prompts and streams the agent's events back.
func newRCCmd() *cobra.Command {
	var (
		server, token, name, workdir      string
		provName, model, baseURL, apiKey  string
		workerProv, workerModel, workerBU string
	)
	cmd := &cobra.Command{
		Use:   "rc",
		Short: "Link this computer to a holocron server so the phone can drive it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := resolveProviders(cmd, provName, model)
			if err != nil {
				return err
			}
			lead, err := provider.New(res.Lead.Provider, provider.Config{BaseURL: res.Lead.BaseURL, APIKey: res.Lead.APIKey})
			if err != nil {
				return fmt.Errorf("provider: %w", err)
			}
			var worker provider.Provider
			if res.WorkerEnabled {
				worker, _ = provider.New(res.Worker.Provider, provider.Config{BaseURL: res.Worker.BaseURL, APIKey: res.Worker.APIKey})
			}
			if name == "" {
				name, _ = os.Hostname()
			}
			if name == "" {
				name = "device"
			}
			abs, _ := filepath.Abs(workdir)

			d := &device{
				name: name, workdir: abs, repo: repoLabel(workdir),
				lead: lead, leadModel: res.Lead.Model, worker: worker, workerModel: res.Worker.Model,
				runs: map[string]context.CancelFunc{},
			}

			url := strings.TrimRight(server, "/") + "/api/device/connect"
			url = strings.Replace(strings.Replace(url, "https://", "wss://", 1), "http://", "ws://", 1)
			opts := &websocket.DialOptions{}
			if token != "" {
				opts.HTTPHeader = map[string][]string{"Authorization": {"Bearer " + token}}
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "linking %q to %s …\n", name, server)
			ctx := cmd.Context()
			c, _, err := websocket.Dial(ctx, url, opts)
			if err != nil {
				return fmt.Errorf("dial %s: %w", url, err)
			}
			defer c.Close(websocket.StatusNormalClosure, "")
			c.SetReadLimit(4 << 20)
			d.c = c

			if err := d.send(ctx, relay.Frame{Type: "hello", Device: name}); err != nil {
				return err
			}
			// Advertise one session for this workspace so it appears on the phone.
			d.startSession(ctx, "ses_"+name)
			fmt.Fprintf(out, "linked. this computer is now drivable from the phone (device %q).\n", name)

			for {
				var f relay.Frame
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
		},
	}
	f := cmd.Flags()
	f.StringVar(&server, "server", "", "holocron server URL (e.g. https://holocron.example.com)")
	f.StringVar(&token, "token", "", "device bearer token (server auth)")
	f.StringVar(&name, "name", "", "device name (default: hostname)")
	f.StringVarP(&workdir, "workdir", "C", ".", "workspace root sessions run in")
	f.StringVar(&provName, "provider", "anthropic", "lead backend")
	f.StringVar(&model, "model", defaultModel, "lead model")
	f.StringVar(&baseURL, "base-url", "", "lead provider base URL")
	f.StringVar(&apiKey, "api-key", "", "lead provider API key")
	f.StringVar(&workerProv, "worker-provider", "openai", "worker backend")
	f.StringVar(&workerModel, "worker-model", "", "worker model id (enables delegation)")
	f.StringVar(&workerBU, "worker-base-url", "", "worker provider base URL")
	return cmd
}

// device is the linked-computer side of the relay.
type device struct {
	name, workdir, repo string
	lead                provider.Provider
	leadModel           string
	worker              provider.Provider
	workerModel         string
	c                   *websocket.Conn
	sendMu              sync.Mutex
	mu                  sync.Mutex
	runs                map[string]context.CancelFunc // sessionID -> cancel
}

func (d *device) send(ctx context.Context, f relay.Frame) error {
	d.sendMu.Lock()
	defer d.sendMu.Unlock()
	return wsjson.Write(ctx, d.c, f)
}

// startSession advertises an idle session to the server.
func (d *device) startSession(ctx context.Context, id string) {
	_ = d.send(ctx, relay.Frame{Type: "session", Session: &relay.SessionInfo{
		ID: id, Model: d.leadModel, Provider: d.lead.Name(),
		Workspace: d.repo, Status: "idle",
	}})
}

// run executes a prompt in a session locally and streams its events back.
func (d *device) run(ctx context.Context, id, text string) {
	runCtx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.runs[id] = cancel
	d.mu.Unlock()

	d.send(ctx, relay.Frame{Type: "session", Session: &relay.SessionInfo{
		ID: id, Model: d.leadModel, Provider: d.lead.Name(),
		Workspace: d.repo, Status: "running", Title: firstLine(text),
	}})
	d.send(ctx, relay.Frame{Type: "event", SessionID: id, Kind: "prompt", Data: text})

	opts := []agent.Option{agent.WithEventHook(func(e agent.Event) {
		d.send(ctx, relay.Frame{Type: "event", SessionID: id, Kind: e.Kind, Data: e.Data})
	})}
	if d.worker != nil && d.workerModel != "" {
		opts = append(opts, agent.WithWorker(d.worker, d.workerModel))
	}
	ag, err := agent.New(agent.Config{Name: "holocron", Model: d.leadModel, Workdir: d.workdir}, d.lead, opts...)
	if err != nil {
		d.send(ctx, relay.Frame{Type: "event", SessionID: id, Kind: "error", Data: err.Error()})
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
			d.send(ctx, relay.Frame{Type: "event", SessionID: id, Kind: "error", Data: err.Error()})
			return
		}
		d.send(ctx, relay.Frame{Type: "event", SessionID: id, Kind: "done",
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
