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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dougkirkley/ergon/pkg/provider"
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
	Kind  string // "text" | "tool" | "tool-result" | "edit" | "usage" | "review" | "error" | "worker:*"
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

// WithMaxParallel caps how many worker subagents run concurrently when the lead
// delegates a batch of tasks (default 2). Only independent tasks should be run
// in parallel — workers share the workspace.
func WithMaxParallel(n int) Option {
	return func(a *Agent) {
		if n > 0 {
			a.maxParallel = n
		}
	}
}

// AskQuestion is one question in an ask_user request. Options makes it a
// single-choice pick; empty Options means free text.
type AskQuestion struct {
	Header      string   `json:"header"`
	Question    string   `json:"question"`
	Options     []string `json:"options"`
	MultiSelect bool     `json:"multiSelect"`
}

// AskRequest is one or more questions posed to the user at once (Claude-style
// multi-question picker with tabs).
type AskRequest struct {
	Questions []AskQuestion
}

// AskFunc asks the user the questions and returns their answers as text.
// Optional; enables the ask_user tool.
type AskFunc func(AskRequest) (string, error)

// WithAsker gives the agent an `ask_user` tool that asks the user a clarifying
// question via fn and feeds their answer back to the model. Without it, the tool
// is not offered.
func WithAsker(fn AskFunc) Option { return func(a *Agent) { a.asker = fn } }

// PermMode gates which tools run without asking. Enforcement lives in the agent
// loop, so every client (TUI, serve, mobile) gets it.
type PermMode string

const (
	PermAuto        PermMode = ""             // run everything freely
	PermManual      PermMode = "manual"       // ask before any edit or command
	PermAcceptEdits PermMode = "accept edits" // auto edits; ask before commands
	PermPlan        PermMode = "plan"         // read-only: block edits and commands
)

// WithPermission sets the permission mode. Gating uses the asker (WithAsker) to
// confirm; with no asker configured, gating allows by default (non-interactive
// callers stay unblocked). Auto/unknown means no gating.
func WithPermission(mode string) Option {
	return func(a *Agent) { a.permMode = PermMode(strings.ToLower(strings.TrimSpace(mode))) }
}

// WithConversation keeps the provider session alive across Run calls, so the
// agent remembers prior turns (a persistent conversation). Without it, each Run
// starts fresh — the default for one-shot and server use.
func WithConversation() Option { return func(a *Agent) { a.conv = true } }

// ToolPermission is a per-tool gating decision from config: run freely, ask
// first, or refuse.
type ToolPermission string

const (
	ToolAllow ToolPermission = "allow"
	ToolAsk   ToolPermission = "ask"
	ToolDeny  ToolPermission = "deny"
)

// WithAllowedTools restricts the agent to exactly the named tools; anything not
// listed is disabled. An empty list means all tools are enabled. Names are the
// tool ids (read, write, edit, bash, monitor, shell_output, delegate, publish,
// ask_user, web_search, …) and any plugin tools.
func WithAllowedTools(names []string) Option {
	return func(a *Agent) {
		if len(names) == 0 {
			return
		}
		a.allowTools = map[string]bool{}
		for _, n := range names {
			a.allowTools[n] = true
		}
	}
}

// PluginSpec defines an external tool the agent can call without recompiling:
// the model calls Name with JSON args, and the agent runs Command with those
// args as JSON on stdin, returning the command's output. This is how tools are
// imported as plugins (config-driven, MCP-lite).
type PluginSpec struct {
	Name        string
	Description string
	Command     []string       // argv; the tool's JSON input is written to stdin
	Properties  map[string]any // JSON-schema properties for the args
	Required    []string
}

// WithPluginTools registers external command tools (see PluginSpec).
func WithPluginTools(specs []PluginSpec) Option {
	return func(a *Agent) {
		if len(specs) == 0 {
			return
		}
		a.plugins = map[string]PluginSpec{}
		for _, s := range specs {
			if s.Name != "" && len(s.Command) > 0 {
				a.plugins[s.Name] = s
			}
		}
	}
}

// SkillSpec is a model-invocable skill: its Name+Description are advertised to
// the model via the `skill` tool, and Body (the instructions) is returned only
// when the model loads it by name. This is progressive disclosure — a skill's
// instructions enter context on demand, not up front.
type SkillSpec struct {
	Name        string
	Description string
	Body        string
}

// ToolHandler runs a host-registered tool: it receives the model's raw JSON
// input and returns the result plus whether it is an error.
type ToolHandler func(ctx context.Context, input json.RawMessage) (string, bool)

type extraTool struct {
	def provider.ToolDef
	h   ToolHandler
}

// WithTool registers a host-defined tool (its schema and a Go handler). This is
// how the embedding program adds first-class tools without a subprocess — e.g.
// the CLI wires a `config` tool that edits ergon's own settings.
func WithTool(def provider.ToolDef, h ToolHandler) Option {
	return func(a *Agent) {
		if def.Name == "" || h == nil {
			return
		}
		if a.extraTools == nil {
			a.extraTools = map[string]extraTool{}
		}
		a.extraTools[def.Name] = extraTool{def: def, h: h}
	}
}

// WithSkills advertises skills to the model through a `skill` tool.
func WithSkills(specs []SkillSpec) Option {
	return func(a *Agent) {
		a.skills = nil
		for _, s := range specs {
			if s.Name != "" && s.Body != "" {
				a.skills = append(a.skills, s)
			}
		}
	}
}

// PermRuleSpec is a per-tool permission rule, opencode-style: either a blanket
// Decision (allow|ask|deny) for the tool, or, for command tools like bash, a map
// of command glob → decision (e.g. {"git push": "ask", "rm *": "deny", "*":
// "allow"}).
type PermRuleSpec struct {
	Decision string
	Patterns map[string]string
}

type permRule struct {
	blanket  ToolPermission
	patterns map[string]ToolPermission
}

// WithPermissionRules sets per-tool gating rules that override the permission
// mode. Command tools match the command against glob patterns.
func WithPermissionRules(rules map[string]PermRuleSpec) Option {
	return func(a *Agent) {
		if len(rules) == 0 {
			return
		}
		a.permRules = map[string]permRule{}
		norm := func(s string) ToolPermission { return ToolPermission(strings.ToLower(strings.TrimSpace(s))) }
		for tool, spec := range rules {
			r := permRule{blanket: norm(spec.Decision)}
			if len(spec.Patterns) > 0 {
				r.patterns = map[string]ToolPermission{}
				for pat, dec := range spec.Patterns {
					r.patterns[pat] = norm(dec)
				}
			}
			a.permRules[tool] = r
		}
	}
}

// SetEventHook, SetPermission, and ResetConversation let a long-lived agent (see
// WithConversation) be re-tuned between turns without rebuilding it.
func (a *Agent) SetEventHook(fn func(Event)) { a.hook = fn }
func (a *Agent) SetPermission(mode string) {
	a.permMode = PermMode(strings.ToLower(strings.TrimSpace(mode)))
}
func (a *Agent) ResetConversation() { a.sess = nil }

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

// WithReviewer enables the code-review skill: after a run makes file changes, a
// separate model (typically a stronger one than did the edits) reviews the
// collected diffs and its findings are emitted as a "review" event and returned
// in Result.Review. Usage is tracked under the reviewer model.
func WithReviewer(p provider.Provider, model string) Option {
	return func(a *Agent) { a.reviewer = &reviewer{prov: p, model: model} }
}

// WithCompactor enables the token-efficiency skill: tool results larger than
// threshold bytes are compressed by a cheap model before they enter the lead
// model's message history, so a big read/bash output is summarized once instead
// of re-sent verbatim on every subsequent turn. A threshold <= 0 uses the
// default. Compactor usage is tracked under its own model. Error results and
// small results are never compacted.
func WithCompactor(p provider.Provider, model string, threshold int) Option {
	if threshold <= 0 {
		threshold = defaultCompactThreshold
	}
	return func(a *Agent) {
		a.compactor = &compactor{prov: p, model: model, threshold: threshold}
	}
}

// CompactRecord is one compaction observation, handed to a CompactStatsFunc so
// callers can measure whether a persistent content-hash cache would pay off:
// repeated Hashes across runs are cache hits a cache would have served.
type CompactRecord struct {
	Hash     string // sha256 of the raw tool result
	Tool     string
	RawBytes int
	OutBytes int // bytes actually forwarded to the lead (raw if compaction didn't help)
}

// CompactStatsFunc receives one record per compaction attempt over threshold.
type CompactStatsFunc func(CompactRecord)

// WithCompactStats records each compaction so the value of a cross-session
// summary cache can be measured before building one.
func WithCompactStats(fn CompactStatsFunc) Option {
	return func(a *Agent) { a.compactStats = fn }
}

type worker struct {
	prov  provider.Provider
	model string
}

type reviewer struct {
	prov  provider.Provider
	model string
}

type compactor struct {
	prov      provider.Provider
	model     string
	threshold int
}

// editRec is one file change captured during a run, for the reviewer skill.
type editRec struct {
	file string
	diff string
}

// Agent is a configured, runnable coding agent. Construct with New.
type Agent struct {
	cfg          Config
	prov         provider.Provider
	ws           *workspace
	out          io.Writer
	hook         func(Event)
	worker       *worker
	reviewer     *reviewer
	compactor    *compactor
	compactStats CompactStatsFunc
	maxParallel  int       // max concurrent worker subagents (0 = default 2)
	edits        []editRec // changes made this run, for the reviewer skill
	publish      PublishFunc
	asker        AskFunc
	permMode     PermMode
	allowTools   map[string]bool       // if non-empty, only these tools are enabled
	permRules    map[string]permRule   // per-tool allow/ask/deny overrides
	plugins      map[string]PluginSpec // external command tools
	skills       []SkillSpec           // model-invocable skills (progressive disclosure)
	extraTools   map[string]extraTool  // host-registered tools (WithTool)
	conv         bool                  // keep the session across Run calls
	sess         provider.Session      // persisted session when conv is set
	resume       string
	checkpoint   func(brief string) error
	emitMu       sync.Mutex // serializes emit across parallel workers

	shellMu  sync.Mutex // guards the background-shell registry
	shells   []*shell
	shellSeq int
	bgReq    chan struct{} // ctrl+b: convert the running foreground bash to background
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
	a := &Agent{cfg: cfg, prov: p, ws: ws, bgReq: make(chan struct{}, 1)}
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
	Review  string                    // reviewer findings, if a reviewer was set
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
	// In conversation mode the session persists across Run calls so prior turns
	// are remembered; otherwise each Run starts fresh.
	sess := a.sess
	fresh := sess == nil
	if fresh {
		sess = a.prov.NewSession(provider.SessionConfig{
			System:    systemPrompt(a.cfg, a.worker != nil, a.publish != nil),
			Tools:     a.enabledTools(),
			Model:     a.cfg.Model,
			MaxTokens: a.cfg.MaxTokens,
		})
		if a.conv {
			a.sess = sess
		}
	}

	res := &Result{}
	in := provider.Input{Text: prompt}
	if fresh {
		in.Text = seedPrompt(a.resume, prompt) // resume brief only seeds a new session
	}

	done := false
	for res.Turns < maxTurns && !done {
		resp, err := sess.Send(ctx, in)
		if err != nil {
			return res, a.friendlyErr(err)
		}
		res.Turns++
		res.add(a.cfg.Model, resp.Usage)
		a.emitUsage(res)

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
			if deny, ok := a.gate(tc); !ok {
				a.emit("tool-result", deny)
				results = append(results, provider.ToolResult{ID: tc.ID, Content: deny})
				continue
			}
			content, isErr := a.execTool(ctx, tc, res)
			if isErr {
				a.emit("error", content)
			} else {
				content = a.maybeCompact(ctx, tc.Name, content, res)
				// Emit a generous slice so clients can expand the full output
				// (the feed still shows a one-line preview); cap to bound payload.
				a.emit("tool-result", truncate(content, 16000))
			}
			results = append(results, provider.ToolResult{ID: tc.ID, Content: content, IsError: isErr})
		}
		in = provider.Input{ToolResults: results}
	}
	if !done {
		return res, fmt.Errorf("agent %q: hit turn limit (%d)", a.cfg.Name, maxTurns)
	}

	// Code-review skill: a separate model reviews the diffs this run produced.
	a.runReview(ctx, res)

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

// friendlyErr turns known provider errors into actionable messages. Notably,
// many local models lack tool calling, which the agent loop requires.
func (a *Agent) friendlyErr(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "does not support tools") {
		return fmt.Errorf("model %q can't run agent sessions: it has no tool-calling support. Pick a tool-capable model (e.g. qwen3-coder, qwen2.5-coder, llama3.1)", a.cfg.Model)
	}
	return err
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
	a.emitMu.Lock() // serialize across parallel workers
	defer a.emitMu.Unlock()
	if a.out != nil {
		fmt.Fprintf(a.out, "[%s/%s] %s\n", a.cfg.Name, kind, data)
	}
	if a.hook != nil {
		a.hook(Event{Agent: a.cfg.Name, Kind: kind, Data: data})
	}
}

// emitEdit emits a structured "edit" event describing a write/edit: the file,
// added/removed line counts, and a unified diff. Clients render it as an
// activity line ("Edited file +A -B") that expands to the diff.
func (a *Agent) emitEdit(file, oldText, newText string) {
	added, removed, diff := diffStat(oldText, newText)
	a.edits = append(a.edits, editRec{file: file, diff: diff})
	payload, err := json.Marshal(map[string]any{
		"file": file, "added": added, "removed": removed, "diff": diff,
	})
	if err != nil {
		return
	}
	a.emit("edit", string(payload))
}

// emitUsage emits the run's cumulative token usage so a client can show a live
// token counter (like Claude Code's).
func (a *Agent) emitUsage(res *Result) {
	payload, err := json.Marshal(map[string]any{
		"in":  res.Usage.InputTokens,
		"out": res.Usage.OutputTokens,
	})
	if err != nil {
		return
	}
	a.emit("usage", string(payload))
}

const defaultCompactThreshold = 6000 // bytes; tool results larger get compacted

const compactorSystem = "You compress a tool result for a coding agent to save " +
	"tokens. Preserve every fact the agent could need: exact file paths, symbol " +
	"and identifier names, function signatures, error messages, line numbers, " +
	"counts, and values, verbatim. Drop only redundancy, boilerplate, and " +
	"repeated framing. Never invent or infer. Output the condensed result only, " +
	"no preamble."

// maybeCompact compresses an oversized tool result through the cheap compactor
// model so the lead model's history carries a summary instead of the raw blob.
// It is a no-op unless a compactor is configured and the result exceeds the
// threshold. On any error it returns the original content unchanged.
func (a *Agent) maybeCompact(ctx context.Context, tool, content string, res *Result) string {
	if a.compactor == nil || len(content) < a.compactor.threshold {
		return content
	}
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])
	record := func(out int) {
		if a.compactStats != nil {
			a.compactStats(CompactRecord{Hash: hash, Tool: tool, RawBytes: len(content), OutBytes: out})
		}
	}
	sess := a.compactor.prov.NewSession(provider.SessionConfig{
		System: compactorSystem,
		Model:  a.compactor.model,
	})
	resp, err := sess.Send(ctx, provider.Input{
		Text: fmt.Sprintf("Tool: %s\nResult to compress:\n\n%s", tool, content),
	})
	if err != nil || resp.Text == "" {
		record(len(content))
		return content
	}
	res.add(a.compactor.model, resp.Usage)
	if len(resp.Text) >= len(content) {
		record(len(content))
		return content // compaction didn't help; keep the original
	}
	record(len(resp.Text))
	a.emit("compact", fmt.Sprintf("%s result %d -> %d bytes", tool, len(content), len(resp.Text)))
	return resp.Text + "\n\n[compacted from " + fmt.Sprintf("%d", len(content)) + " bytes]"
}

const reviewerSystem = "You are a senior staff engineer doing a focused code " +
	"review. Judge only the diffs shown. Flag real bugs, security issues, and " +
	"regressions with the file and a concrete fix; skip style nits. Be terse. " +
	"If nothing is wrong, reply exactly: LGTM."

// runReview is the code-review skill: a separate model reviews the diffs made
// this run. No-op unless a reviewer is configured and at least one edit landed.
func (a *Agent) runReview(ctx context.Context, res *Result) {
	if a.reviewer == nil || len(a.edits) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("Review these changes:\n\n")
	for _, e := range a.edits {
		b.WriteString("=== " + e.file + " ===\n" + e.diff + "\n")
	}
	sess := a.reviewer.prov.NewSession(provider.SessionConfig{
		System:    reviewerSystem,
		Model:     a.reviewer.model,
		MaxTokens: a.cfg.MaxTokens,
	})
	resp, err := sess.Send(ctx, provider.Input{Text: b.String()})
	if err != nil {
		a.emit("error", "review: "+err.Error())
		return
	}
	res.add(a.reviewer.model, resp.Usage)
	res.Review = resp.Text
	a.emit("review", resp.Text)
}

// toolEnabled reports whether a tool is enabled. With a non-empty allow-list,
// only listed tools are enabled; otherwise all are.
func (a *Agent) toolEnabled(name string) bool {
	if len(a.allowTools) == 0 {
		return true
	}
	return a.allowTools[name]
}

// resolvePerm resolves a per-tool rule to a decision: a blanket decision, or —
// for command tools — the decision for the first matching command glob (else the
// "*" catch-all). Empty means "no rule matched; use the mode".
func (a *Agent) resolvePerm(r permRule, tc provider.ToolCall) ToolPermission {
	if r.blanket != "" {
		return r.blanket
	}
	if len(r.patterns) == 0 {
		return ""
	}
	cmd := commandArg(tc)
	for pat, dec := range r.patterns {
		if pat == "*" {
			continue
		}
		if ok, _ := filepath.Match(pat, cmd); ok {
			return dec
		}
		if star := strings.TrimSuffix(pat, "*"); star != pat && strings.HasPrefix(cmd, star) {
			return dec // prefix glob fallback (filepath.Match won't cross "/")
		}
	}
	if d, ok := r.patterns["*"]; ok {
		return d
	}
	return ""
}

// commandArg extracts the "command" argument (bash/monitor and command plugins).
func commandArg(tc provider.ToolCall) string {
	var in map[string]any
	_ = json.Unmarshal(tc.Input, &in)
	if c, ok := in["command"].(string); ok {
		return c
	}
	return ""
}

// enabledTools returns the offered tool defs (built-ins + plugins) after
// applying the config filter.
func (a *Agent) enabledTools() []provider.ToolDef {
	defs := toolDefs(a.worker != nil, a.publish != nil, a.asker != nil)
	for _, p := range a.plugins {
		defs = append(defs, provider.ToolDef{
			Name: p.Name, Description: p.Description, Properties: p.Properties, Required: p.Required,
		})
	}
	for _, et := range a.extraTools {
		defs = append(defs, et.def)
	}
	if names := a.skillNames(); len(names) > 0 {
		defs = append(defs, provider.ToolDef{
			Name:        "skill",
			Description: a.skillToolDescription(),
			Properties: map[string]any{
				"name": map[string]any{"type": "string", "description": "the skill to load", "enum": names},
			},
			Required: []string{"name"},
		})
	}
	if len(a.allowTools) == 0 {
		return defs
	}
	out := defs[:0]
	for _, d := range defs {
		if a.toolEnabled(d.Name) {
			out = append(out, d)
		}
	}
	return out
}

// skillNames returns the advertised skill names, in order.
func (a *Agent) skillNames() []string {
	if len(a.skills) == 0 {
		return nil
	}
	names := make([]string, len(a.skills))
	for i, s := range a.skills {
		names[i] = s.Name
	}
	return names
}

// skillToolDescription lists the available skills so the model knows when to
// load one (progressive disclosure — bodies are fetched via the tool).
func (a *Agent) skillToolDescription() string {
	var b strings.Builder
	b.WriteString("Load a skill's full instructions on demand, then follow them. Call this when a request matches a skill below. Available skills:\n")
	for _, s := range a.skills {
		b.WriteString("- " + s.Name)
		if s.Description != "" {
			b.WriteString(": " + s.Description)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// runSkill returns a skill's body (its instructions) to the model.
func (a *Agent) runSkill(input json.RawMessage) (string, bool) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return err.Error(), true
	}
	for _, s := range a.skills {
		if s.Name == in.Name {
			return s.Body, false
		}
	}
	return fmt.Sprintf("no skill named %q", in.Name), true
}

// runPlugin executes an external command tool: the model's JSON args go to the
// command's stdin; its combined output is returned to the model.
func (a *Agent) runPlugin(ctx context.Context, p PluginSpec, input json.RawMessage) (string, bool) {
	cmd := exec.CommandContext(ctx, p.Command[0], p.Command[1:]...)
	cmd.Dir = a.ws.root
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("%s\n[plugin %s error: %v]", strings.TrimSpace(string(out)), p.Name, err), false
	}
	if strings.TrimSpace(string(out)) == "" {
		return "[no output]", false
	}
	return string(out), false
}

// gate applies the tool filter, per-tool permission rules, then the permission
// mode. It returns (denialMessage, false) to block a tool (fed back to the model
// as the tool result), or ("", true) to allow it.
func (a *Agent) gate(tc provider.ToolCall) (string, bool) {
	if !a.toolEnabled(tc.Name) {
		return "the " + tc.Name + " tool is disabled by configuration.", false
	}
	// Per-tool rules from config override the mode for that tool.
	if r, ok := a.permRules[tc.Name]; ok {
		switch a.resolvePerm(r, tc) {
		case ToolAllow:
			return "", true
		case ToolDeny:
			return "the " + tc.Name + " tool is denied by configuration.", false
		case ToolAsk:
			if !a.approve(tc) {
				return "denied by the user.", false
			}
			return "", true
		}
		// empty decision (no pattern matched) → fall through to mode gating
	}
	writes := tc.Name == "write" || tc.Name == "edit"
	commands := tc.Name == "bash" || tc.Name == "monitor"
	if !writes && !commands {
		return "", true // read, shell_output, delegate, publish, ask_user: never gated
	}
	switch a.permMode {
	case PermPlan:
		return "blocked: plan mode is read-only — no edits or commands. Describe your plan and ask the user to switch modes to apply it.", false
	case PermManual:
		if !a.approve(tc) {
			return "denied by the user.", false
		}
	case PermAcceptEdits:
		if commands && !a.approve(tc) {
			return "denied by the user.", false
		}
	}
	return "", true
}

// approve asks the user to allow a gated tool call. With no asker (non-
// interactive), it allows by default so servers and one-shot runs aren't blocked.
func (a *Agent) approve(tc provider.ToolCall) bool {
	if a.asker == nil {
		return true
	}
	ans, err := a.asker(AskRequest{Questions: []AskQuestion{{
		Header:   tc.Name,
		Question: "Allow this " + tc.Name + "?\n" + gateSummary(tc),
		Options:  []string{"Allow", "Deny"},
	}}})
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(ans), "allow")
}

// gateSummary is a preview of what a tool call will do, shown in the approval
// prompt so the user can accept or deny with the actual change in view.
func gateSummary(tc provider.ToolCall) string {
	var in map[string]any
	_ = json.Unmarshal(tc.Input, &in)
	s := func(k string) string { v, _ := in[k].(string); return v }
	switch tc.Name {
	case "bash", "monitor":
		return truncate(s("command"), 400)
	case "write":
		return s("path") + "\n" + truncate(s("content"), 600)
	case "edit":
		return s("path") + "\n- " + truncate(s("old_str"), 300) + "\n+ " + truncate(s("new_str"), 300)
	}
	return truncate(string(tc.Input), 200)
}

// execTool dispatches a tool call. Delegation merges the worker's usage into res
// so the run's cost reflects both models.
func (a *Agent) execTool(ctx context.Context, tc provider.ToolCall, res *Result) (string, bool) {
	if p, ok := a.plugins[tc.Name]; ok {
		return a.runPlugin(ctx, p, tc.Input)
	}
	if et, ok := a.extraTools[tc.Name]; ok {
		return et.h(ctx, tc.Input)
	}
	if tc.Name == "skill" {
		return a.runSkill(tc.Input)
	}
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
		old, _ := a.ws.readFile(in.Path) // "" if new file
		out, err := a.ws.writeFile(in.Path, in.Content)
		if err == nil {
			a.emitEdit(in.Path, old, in.Content)
		}
		return orErr(out, err)
	case "edit":
		var in struct {
			Path   string `json:"path"`
			OldStr string `json:"old_str"`
			NewStr string `json:"new_str"`
		}
		if err := json.Unmarshal(tc.Input, &in); err != nil {
			return err.Error(), true
		}
		old, _ := a.ws.readFile(in.Path)
		out, err := a.ws.editFile(in.Path, in.OldStr, in.NewStr)
		if err == nil {
			a.emitEdit(in.Path, old, strings.Replace(old, in.OldStr, in.NewStr, 1))
		}
		return orErr(out, err)
	case "bash":
		var in struct {
			Command    string `json:"command"`
			Background bool   `json:"background"`
		}
		if err := json.Unmarshal(tc.Input, &in); err != nil {
			return err.Error(), true
		}
		if in.Background {
			return a.startBackground(ctx, in.Command)
		}
		return a.runForegroundBash(ctx, in.Command)
	case "shell_output":
		var in struct {
			ID   string `json:"id"`
			Kill bool   `json:"kill"`
		}
		if err := json.Unmarshal(tc.Input, &in); err != nil {
			return err.Error(), true
		}
		return a.shellOutput(in.ID, in.Kill)
	case "monitor":
		var in struct {
			Command  string      `json:"command"`
			Interval flexSeconds `json:"interval_seconds"`
			Timeout  flexSeconds `json:"timeout_seconds"`
		}
		if err := json.Unmarshal(tc.Input, &in); err != nil {
			return err.Error(), true
		}
		return orErr(a.ws.monitor(in.Command,
			in.Interval.durationOr(5*time.Second),
			in.Timeout.durationOr(5*time.Minute)))
	case "web_fetch":
		var in struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(tc.Input, &in); err != nil {
			return err.Error(), true
		}
		return orErr(webFetch(ctx, in.URL))
	case "delegate":
		var in struct {
			Task  string   `json:"task"`
			Tasks []string `json:"tasks"`
		}
		if err := json.Unmarshal(tc.Input, &in); err != nil {
			return err.Error(), true
		}
		if len(in.Tasks) > 0 {
			return a.delegateParallel(ctx, in.Tasks, res)
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
	case "ask_user":
		var in struct {
			Questions []AskQuestion `json:"questions"`
			// single-question convenience form
			Question string   `json:"question"`
			Options  []string `json:"options"`
		}
		if err := json.Unmarshal(tc.Input, &in); err != nil {
			return err.Error(), true
		}
		qs := in.Questions
		if len(qs) == 0 && in.Question != "" {
			qs = []AskQuestion{{Question: in.Question, Options: in.Options}}
		}
		if len(qs) == 0 {
			return "ask_user requires a question", true
		}
		a.emit("ask", qs[0].Question)
		if a.asker == nil {
			return "No interactive user is available; proceed with your best assumption and state it.", false
		}
		ans, err := a.asker(AskRequest{Questions: qs})
		if err != nil {
			return "ask_user failed: " + err.Error(), true
		}
		a.emit("answer", ans)
		return ans, false
	default:
		return fmt.Sprintf("unknown tool %q", tc.Name), true
	}
}

// delegate runs a bounded subtask on the worker model and returns its
// conclusion. The worker shares the workspace but has no delegate tool of its
// own (one level deep). Its usage is merged into res for correct cost.
func (a *Agent) delegate(ctx context.Context, task string, res *Result) (string, bool) {
	sr, concl, isErr := a.delegateOne(ctx, task, workerName(task, 0))
	if sr != nil {
		res.merge(sr)
	}
	return concl, isErr
}

// workerTagSep separates a worker's name from its event payload, so a name may
// contain spaces without confusing the client that renders the subagent tree.
const workerTagSep = "\x1f"

// workerName derives a short, human label for a subagent from its task, e.g.
// "migrate the application chart" -> "migrate the application". Falls back to
// "worker N" (1-based) when the task yields nothing usable.
func workerName(task string, idx int) string {
	task = strings.TrimSpace(oneLineTask(task))
	fields := strings.Fields(task)
	if len(fields) > 4 {
		fields = fields[:4]
	}
	name := strings.Join(fields, " ")
	if len(name) > 32 {
		name = strings.TrimSpace(name[:32])
	}
	if name == "" {
		return fmt.Sprintf("worker %d", idx+1)
	}
	return name
}

func oneLineTask(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// delegateOne runs one worker subagent and returns its Result (for the caller to
// merge), its conclusion text, and whether it errored. name labels the subagent
// and prefixes its worker events so the client can group and title each one.
func (a *Agent) delegateOne(ctx context.Context, task, name string) (*Result, string, bool) {
	if a.worker == nil {
		return nil, "delegation is not configured for this agent", true
	}
	sub, err := New(Config{
		Name:     a.cfg.Name + "/" + name,
		Identity: workerIdentity,
		Model:    a.worker.model,
		Workdir:  a.ws.root,
	}, a.worker.prov, WithEventHook(func(e Event) {
		a.emit("worker:"+e.Kind, name+workerTagSep+e.Data)
	}))
	if err != nil {
		return nil, err.Error(), true
	}
	sr, err := sub.Run(ctx, task)
	if err != nil {
		return sr, fmt.Sprintf("worker failed: %v", err), true
	}
	return sr, sr.Text, false
}

// delegateParallel runs several worker subtasks concurrently, bounded by
// maxParallel, and returns their conclusions in order. Tasks should be
// independent — workers share the workspace.
func (a *Agent) delegateParallel(ctx context.Context, tasks []string, res *Result) (string, bool) {
	if a.worker == nil {
		return "delegation is not configured for this agent", true
	}
	max := a.maxParallel
	if max < 1 {
		max = 2
	}
	sem := make(chan struct{}, max)
	var wg sync.WaitGroup
	var mu sync.Mutex
	conclusions := make([]string, len(tasks))
	a.emit("text", fmt.Sprintf("delegating %d subtasks (max %d parallel)", len(tasks), max))
	for i, task := range tasks {
		wg.Add(1)
		go func(i int, task string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			sr, concl, _ := a.delegateOne(ctx, task, workerName(task, i))
			mu.Lock()
			if sr != nil {
				res.merge(sr)
			}
			conclusions[i] = concl
			mu.Unlock()
		}(i, task)
	}
	wg.Wait()
	var b strings.Builder
	for i, c := range conclusions {
		fmt.Fprintf(&b, "[subtask %d] %s\n", i+1, c)
	}
	return b.String(), false
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
