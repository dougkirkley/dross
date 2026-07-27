// Package agent is an importable coding agent: a provider-agnostic tool loop
// that reads, edits, writes, and runs bash within a confined workspace, and can
// delegate bounded subtasks to a cheaper "worker" model.
//
// An Agent is configured, not hardcoded — give it a Config (name, identity,
// model) and a provider, and construct as many differently-configured agents as
// you like:
//
//	lead, _ := provider.New("anthropic", provider.Config{})
//	worker, _ := provider.New("openai", provider.Config{BaseURL: ollamaURL})
//	a, _ := agent.New(agent.Config{
//	    Name: "builder", Model: "claude-opus-5", Workdir: "/workspace/repo",
//	}, lead, agent.WithWorker(worker, "qwen3-coder:30b"))
//	res, _ := a.Run(ctx, "add a --json flag to the CLI")
//	fmt.Printf("$%.4f\n", res.Cost())
//
// With a worker configured the lead gains a `delegate` tool: it hands
// self-contained mechanical subtasks to the worker (which returns a conclusion,
// not file dumps), keeping expensive lead context small. This is the cost stack
// — see DESIGN.md.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/dougkirkley/holocron/internal/provider"
)

// Style controls how verbose the agent's user-facing output is. Terser styles
// spend fewer output tokens — a context-economy lever, not just cosmetics.
type Style string

const (
	StyleNormal  Style = ""        // default prose
	StyleTerse   Style = "terse"   // fragments, no preamble
	StyleCaveman Style = "caveman" // maximally compressed; substance kept verbatim
)

// Config defines a named agent's identity and lead model. Model and Workdir are
// required.
type Config struct {
	Name      string // human label, for identification and event routing
	Identity  string // system-prompt addendum (persona/task framing); optional
	Model     string // lead provider model id (required)
	Workdir   string // root the file/bash tools are confined to (required)
	MaxTokens int    // per-turn output cap; 0 = provider default
	Style     Style  // output verbosity; default StyleNormal
}

// Event is emitted for each step of a run so callers can stream progress.
type Event struct {
	Agent string // Config.Name
	Kind  string // "text" | "tool" | "tool-result" | "error" | "worker:*"
	Data  string
}

// Option customizes an Agent at construction.
type Option func(*Agent)

// WithOutput writes a human-readable trace of the run to w.
func WithOutput(w io.Writer) Option { return func(a *Agent) { a.out = w } }

// WithEventHook is called for every Event during a run.
func WithEventHook(fn func(Event)) Option { return func(a *Agent) { a.hook = fn } }

// WithWorker enables cost-saving delegation: the agent gains a `delegate` tool
// that runs bounded subtasks on p/model (typically a cheaper or local model).
// A worker never gets its own worker — delegation is one level deep.
func WithWorker(p provider.Provider, model string) Option {
	return func(a *Agent) { a.worker = &worker{prov: p, model: model} }
}

// PublishFunc stores a file and returns a shareable URL for it.
type PublishFunc func(name string, data []byte, mime string) (url string, err error)

// WithPublish gives the agent a `publish` tool: it shares a workspace file
// (screenshot, gif, report) via fn and hands the resulting URL back to the
// model, which can surface it to the user.
func WithPublish(fn PublishFunc) Option {
	return func(a *Agent) { a.publish = fn }
}

// WithResume seeds the run with a prior session's handoff brief instead of a
// full transcript — the cheap way to continue work in a fresh session.
func WithResume(brief string) Option {
	return func(a *Agent) { a.resume = brief }
}

// WithCheckpoint makes the agent produce a compact handoff brief at the end of a
// run and hand it to save (e.g. persist it under a session name). The brief is
// what a future WithResume replays.
func WithCheckpoint(save func(brief string) error) Option {
	return func(a *Agent) { a.checkpoint = save }
}

type worker struct {
	prov  provider.Provider
	model string
}

// Agent is a configured, runnable coding agent. Construct with New.
type Agent struct {
	cfg        Config
	prov       provider.Provider
	ws         *workspace
	out        io.Writer
	hook       func(Event)
	worker     *worker
	publish    PublishFunc
	resume     string
	checkpoint func(brief string) error
}

// New builds an agent from cfg using p as its lead model backend.
func New(cfg Config, p provider.Provider, opts ...Option) (*Agent, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("agent %q: Model is required", cfg.Name)
	}
	if cfg.Workdir == "" {
		return nil, fmt.Errorf("agent %q: Workdir is required", cfg.Name)
	}
	ws, err := newWorkspace(cfg.Workdir)
	if err != nil {
		return nil, err
	}
	a := &Agent{cfg: cfg, prov: p, ws: ws}
	for _, o := range opts {
		o(a)
	}
	return a, nil
}

// Name returns the agent's configured name.
func (a *Agent) Name() string { return a.cfg.Name }

// Result summarizes a completed run, with usage tracked per model so cost is
// correct across the lead/worker split.
type Result struct {
	Text    string
	Turns   int
	Usage   provider.Usage            // total across all models
	ByModel map[string]provider.Usage // per-model usage
	Brief   string                    // handoff brief, if a checkpoint was set
}

func (r *Result) add(model string, u provider.Usage) {
	if r.ByModel == nil {
		r.ByModel = map[string]provider.Usage{}
	}
	cur := r.ByModel[model]
	cur.Add(u)
	r.ByModel[model] = cur
	r.Usage.Add(u)
}

func (r *Result) merge(o *Result) {
	for m, u := range o.ByModel {
		r.add(m, u)
	}
}

// Cost estimates total USD across every model the run used.
func (r Result) Cost() float64 {
	var total float64
	for model, u := range r.ByModel {
		total += Cost(model, u)
	}
	return total
}

const maxTurns = 40

// Run drives the agent to completion on prompt.
func (a *Agent) Run(ctx context.Context, prompt string) (*Result, error) {
	sess := a.prov.NewSession(provider.SessionConfig{
		System:    systemPrompt(a.cfg, a.worker != nil, a.publish != nil),
		Tools:     toolDefs(a.worker != nil, a.publish != nil),
		Model:     a.cfg.Model,
		MaxTokens: a.cfg.MaxTokens,
	})

	res := &Result{}
	in := provider.Input{Text: seedPrompt(a.resume, prompt)}

	done := false
	for res.Turns < maxTurns && !done {
		resp, err := sess.Send(ctx, in)
		if err != nil {
			return res, err
		}
		res.Turns++
		res.add(a.cfg.Model, resp.Usage)

		if resp.Text != "" {
			res.Text = resp.Text
			a.emit("text", resp.Text)
		}

		if resp.StopReason != provider.StopToolUse || len(resp.ToolCalls) == 0 {
			done = true
			continue
		}

		results := make([]provider.ToolResult, 0, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			a.emit("tool", fmt.Sprintf("%s %s", tc.Name, string(tc.Input)))
			content, isErr := a.execTool(ctx, tc, res)
			if isErr {
				a.emit("error", content)
			} else {
				a.emit("tool-result", truncate(content, 200))
			}
			results = append(results, provider.ToolResult{ID: tc.ID, Content: content, IsError: isErr})
		}
		in = provider.Input{ToolResults: results}
	}
	if !done {
		return res, fmt.Errorf("agent %q: hit turn limit (%d)", a.cfg.Name, maxTurns)
	}

	// Handoff brief: one extra turn on the same session (full context) asking
	// for a compact resume summary. Cheap, and what a future run replays.
	if a.checkpoint != nil {
		if brief := a.makeBrief(ctx, sess, res); brief != "" {
			if err := a.checkpoint(brief); err != nil {
				a.emit("error", "checkpoint: "+err.Error())
			} else {
				res.Brief = brief
				a.emit("checkpoint", "saved handoff brief")
			}
		}
	}
	return res, nil
}

// makeBrief asks the model to summarize crucial state for a fresh session,
// reusing the live session so it has full context. Counts its own usage.
func (a *Agent) makeBrief(ctx context.Context, sess provider.Session, res *Result) string {
	resp, err := sess.Send(ctx, provider.Input{Text: briefPrompt})
	if err != nil {
		a.emit("error", "brief: "+err.Error())
		return ""
	}
	res.Turns++
	res.add(a.cfg.Model, resp.Usage)
	return resp.Text
}

func seedPrompt(resume, prompt string) string {
	if resume == "" {
		return prompt
	}
	return "You are resuming a prior session. This handoff brief is the only " +
		"record of it — the full transcript is gone:\n\n" + resume +
		"\n\n---\nContinue with this task:\n" + prompt
}

func (a *Agent) emit(kind, data string) {
	if a.out != nil {
		fmt.Fprintf(a.out, "[%s/%s] %s\n", a.cfg.Name, kind, data)
	}
	if a.hook != nil {
		a.hook(Event{Agent: a.cfg.Name, Kind: kind, Data: data})
	}
}

// execTool dispatches a tool call. Delegation merges the worker's usage into res
// so the run's cost reflects both models.
func (a *Agent) execTool(ctx context.Context, tc provider.ToolCall, res *Result) (string, bool) {
	switch tc.Name {
	case "read":
		var in struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(tc.Input, &in); err != nil {
			return err.Error(), true
		}
		return orErr(a.ws.readFile(in.Path))
	case "write":
		var in struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(tc.Input, &in); err != nil {
			return err.Error(), true
		}
		return orErr(a.ws.writeFile(in.Path, in.Content))
	case "edit":
		var in struct {
			Path   string `json:"path"`
			OldStr string `json:"old_str"`
			NewStr string `json:"new_str"`
		}
		if err := json.Unmarshal(tc.Input, &in); err != nil {
			return err.Error(), true
		}
		return orErr(a.ws.editFile(in.Path, in.OldStr, in.NewStr))
	case "bash":
		var in struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(tc.Input, &in); err != nil {
			return err.Error(), true
		}
		return orErr(a.ws.runBash(in.Command))
	case "delegate":
		var in struct {
			Task string `json:"task"`
		}
		if err := json.Unmarshal(tc.Input, &in); err != nil {
			return err.Error(), true
		}
		return a.delegate(ctx, in.Task, res)
	case "publish":
		var in struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(tc.Input, &in); err != nil {
			return err.Error(), true
		}
		return a.doPublish(in.Path)
	default:
		return fmt.Sprintf("unknown tool %q", tc.Name), true
	}
}

// delegate runs a bounded subtask on the worker model and returns its
// conclusion. The worker shares the workspace but has no delegate tool of its
// own (one level deep). Its usage is merged into res for correct cost.
func (a *Agent) delegate(ctx context.Context, task string, res *Result) (string, bool) {
	if a.worker == nil {
		return "delegation is not configured for this agent", true
	}
	sub, err := New(Config{
		Name:     a.cfg.Name + "/worker",
		Identity: workerIdentity,
		Model:    a.worker.model,
		Workdir:  a.ws.root,
	}, a.worker.prov, WithEventHook(func(e Event) {
		a.emit("worker:"+e.Kind, e.Data)
	}))
	if err != nil {
		return err.Error(), true
	}
	sr, err := sub.Run(ctx, task)
	if sr != nil {
		res.merge(sr)
	}
	if err != nil {
		return fmt.Sprintf("worker failed: %v", err), true
	}
	return sr.Text, false
}

// doPublish shares a workspace file and returns its URL to the model.
func (a *Agent) doPublish(path string) (string, bool) {
	if a.publish == nil {
		return "publishing is not configured for this agent", true
	}
	data, mime, name, err := a.ws.readForPublish(path)
	if err != nil {
		return err.Error(), true
	}
	url, err := a.publish(name, data, mime)
	if err != nil {
		return "publish failed: " + err.Error(), true
	}
	return "published " + name + " → " + url, false
}

func orErr(out string, err error) (string, bool) {
	if err != nil {
		return err.Error(), true
	}
	return out, false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
