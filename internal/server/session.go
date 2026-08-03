package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dougkirkley/dross/internal/config"
	"github.com/dougkirkley/dross/internal/relay"
	"github.com/dougkirkley/dross/internal/storage"
	"github.com/dougkirkley/dross/pkg/agent"
	"github.com/dougkirkley/dross/pkg/provider"
)

// maxSessionEvents bounds retained history per session.
const maxSessionEvents = 5000

// Event is one step of a session, with a per-session monotonic Seq so clients
// can reconnect losslessly (fetch history, then tail live, dedupe by Seq).
type Event struct {
	Seq  int       `json:"seq"`
	Kind string    `json:"kind"`
	Data string    `json:"data"`
	Time time.Time `json:"time"`
}

// Session is a server-side, client-independent run context. Its lifetime is not
// tied to any HTTP request — a prompt runs to completion whether or not a client
// is attached, which is what makes remote control work (see DESIGN.md).
type Session struct {
	ID          string `json:"id"`
	Model       string `json:"model"`
	Provider    string `json:"provider"`
	WorkerModel string `json:"worker_model,omitempty"`
	Style       string `json:"style,omitempty"`
	Chat        bool   `json:"chat,omitempty"` // chat session: no file/shell tools

	// owner is the authenticated identity that created the session. Sessions
	// carry the user's prompts, workspace contents and agent output, so they are
	// only ever visible to the identity that owns them.
	owner string

	mu      sync.Mutex
	workdir string
	repo    string // owner/repo (or dir name) label for the session card
	title   string // first prompt, truncated — the session's display name
	// titleAuto marks a name the server invented (the workload's, given at
	// creation) rather than one that means something. The first prompt replaces
	// it, because "add retry to the uploader" says more than "eager-island";
	// a name the user typed is left alone.
	titleAuto bool
	// picked marks that the linked machine has acknowledged the current prompt.
	// It sends a session frame the moment it starts a run, so this arrives long
	// before any model output does — which is what separates "the machine never
	// took the work" from "the model is slow".
	picked    bool
	last      string // most recent user/agent message, for the list preview
	updated   time.Time
	status    string // "idle" | "running" | "error"
	cost      float64
	seq       int
	events    []Event
	subs      map[chan Event]struct{}
	cancel    context.CancelFunc
	mgr       *Manager // back-reference, for marking the session dirty
	device    string   // owning device id ("" = runs in this pod)
	connected bool     // for device sessions: is that device currently linked
	// pendingAsk is the id of the question a device session is blocked on,
	// so a late answer to an abandoned one can be told apart from the
	// answer to the current one.
	pendingAsk string
}

// Status/Cost snapshots (locked reads for the JSON views).
func (s *Session) snapshot() sessionView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sessionView{
		ID: s.ID, Model: s.Model, Provider: s.Provider,
		WorkerModel: s.WorkerModel, Style: s.Style,
		Title: s.title, Workspace: s.repo, Last: s.last, Updated: s.updated,
		Status: s.status, Cost: s.cost, Events: len(s.events),
		Device: s.device, Connected: s.connected,
	}
}

type sessionView struct {
	ID          string    `json:"id"`
	Model       string    `json:"model"`
	Provider    string    `json:"provider"`
	WorkerModel string    `json:"worker_model,omitempty"`
	Style       string    `json:"style,omitempty"`
	Title       string    `json:"title,omitempty"`
	Workspace   string    `json:"workspace,omitempty"`
	Last        string    `json:"last,omitempty"`
	Updated     time.Time `json:"updated,omitempty"`
	Status      string    `json:"status"`
	Cost        float64   `json:"cost"`
	Events      int       `json:"events"`
	Device      string    `json:"device,omitempty"`    // owning device (blank = pod)
	Connected   bool      `json:"connected,omitempty"` // device currently linked
}

// emit appends an event and fans it out to live subscribers.
const (
	// silenceWarning is how long a run may produce nothing before the session
	// says so. Generous: a first turn against a cold local model spends a
	// minute or more loading weights before it emits a token.
	silenceWarning = 45 * time.Second

	// silenceTimeout is how long it may produce nothing before the run is
	// given up on. A model that has answered nothing in this long is not slow,
	// it is not coming — the endpoint is wrong, the backend is wedged, or it is
	// waiting on memory it will not get. Leaving the session "running" for ever
	// hides that behind something that looks like patience.
	//
	// Measured against silence, not total elapsed time: a long agent run is
	// perfectly normal, and emits tool calls and text throughout. Nothing at
	// all is the signal.
	silenceTimeout = 6 * time.Minute
)

// sayIfModelIsCold explains a wait before it happens.
//
// The silence warning covers the same ground eventually, but only after the
// wait has become long enough to worry about - by which point somebody has
// already wondered whether it is broken. Where the answer is knowable it is
// worth saying up front, and where it is not, this says nothing at all.
func (m *Manager) sayIfModelIsCold(s *Session) {
	model := s.Model
	if model == "" {
		model = m.model
	}
	go func() {
		if checkModel(context.Background(), m.leadURL, model) != modelCold {
			return
		}
		// Racing the first token: if the model turned out to be quick, or the
		// run has already said something, this would be noise.
		s.mu.Lock()
		quiet := s.status == "running"
		s.mu.Unlock()
		if !quiet {
			return
		}
		s.emit("meta", "loading "+model+" — it is not in memory yet. "+
			"On this hardware a large local model takes a minute or two to load, "+
			"and the first reply follows once it has.")
	}()
}

// warnIfSilent says something when a run produces nothing for a long time.
//
// A run that is waiting on a model to load looks exactly like one that has
// hung: the status is "running" and the transcript is empty. Saying that
// nothing has come back yet is the difference between waiting and wondering
// whether it is broken.
func (s *Session) warnIfSilent() {
	s.mu.Lock()
	at := s.seq
	s.mu.Unlock()

	time.AfterFunc(silenceWarning, func() {
		if !s.stillSilent(at) {
			return
		}
		s.emit("meta", "still working — nothing back from the model yet. "+
			"A local model that is not loaded can take a minute or two on the first turn.")
	})

	time.AfterFunc(silenceTimeout, func() {
		if !s.stillSilent(at) {
			return
		}
		// Cancel first: the run has to stop before the session is called idle,
		// or a later answer arrives against a session that has moved on.
		s.interrupt()
		s.emit("error", fmt.Sprintf(
			"no response from the model after %s. Check that the configured provider is reachable "+
				"and the model is available; the request was given up on, not answered.",
			silenceTimeout))
		s.setStatus("idle")
	})
}

// pickupTimeout is how long a linked machine has to acknowledge a prompt.
//
// Short, because acknowledging is not work: the device sends a frame as soon as
// it starts the run, before it has asked the model anything. Nothing back in
// this long means the prompt did not arrive — a dropped frame, or a connection
// that is open but no longer carrying anything.
const pickupTimeout = 10 * time.Second

// failIfNotPickedUp gives up on a prompt the linked machine never acknowledged.
func (s *Session) failIfNotPickedUp() {
	time.AfterFunc(pickupTimeout, func() {
		s.mu.Lock()
		stuck := !s.picked && s.status == "running"
		s.mu.Unlock()
		if !stuck {
			return
		}
		s.emit("error", fmt.Sprintf(
			"the session did not pick up the prompt within %s. Its workspace may have "+
				"restarted or lost its connection; the prompt was not run.", pickupTimeout))
		s.setStatus("idle")
	})
}

// markPicked records that the device has acknowledged the running prompt.
func (s *Session) markPicked() {
	s.mu.Lock()
	s.picked = true
	s.mu.Unlock()
}

// stillSilent reports whether the run is going and has produced nothing since
// seq. Both halves matter: a finished run is not silent, it is done.
func (s *Session) stillSilent(seq int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq == seq && s.status == "running"
}

func (s *Session) emit(kind, data string) {
	s.mu.Lock()
	s.seq++
	e := Event{Seq: s.seq, Kind: kind, Data: data, Time: time.Now()}
	s.events = append(s.events, e)
	// Bound the history. A long-running session would otherwise grow without
	// limit, and every reconnect re-serializes the whole slice. Seq stays
	// monotonic, so clients reconnecting with ?after= still work; they just
	// cannot replay beyond the window.
	if len(s.events) > maxSessionEvents {
		drop := len(s.events) - maxSessionEvents
		s.events = append([]Event(nil), s.events[drop:]...)
	}
	s.updated = e.Time
	// Keep a short preview of the latest human/agent message for the list card.
	if (kind == "prompt" || kind == "text") && strings.TrimSpace(data) != "" {
		s.last = truncate(oneLine(data), 140)
	}
	for ch := range s.subs {
		select {
		case ch <- e:
		default: // slow subscriber; history covers the gap on reconnect
		}
	}
	mgr := s.mgr
	s.mu.Unlock()
	if mgr != nil {
		mgr.markDirty(s.ID)
	}
}

// markDevice records that this session's work happens on a linked device (its
// own pod), so prompts are relayed there instead of run in this process.
func (s *Session) markDevice(id string) {
	s.mu.Lock()
	s.device = id
	s.mu.Unlock()
	if s.mgr != nil {
		s.mgr.markDirty(s.ID)
	}
}

// markBusy tells the operator whether this session is mid-turn, so a version
// roll waits rather than taking the pod away part-way through an answer.
//
// In the background: it is a courtesy to the roll, not something a prompt
// should wait on, and the API call is a round trip.
func (m *Manager) markBusy(sessionID string, busy bool) {
	if m.pods == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		m.pods.SetBusy(ctx, sessionID, busy)
	}()
}

// setTitle renames a session for display. The id is untouched: it is what the
// relay device, the workload label and every stored event key on, so a rename
// must not be able to move a session out from under them.
func (s *Session) setTitle(t string) { s.setTitleAuto(t, false) }

// setTitleAuto renames a session, recording whether the name was invented.
func (s *Session) setTitleAuto(t string, auto bool) {
	s.mu.Lock()
	s.title, s.titleAuto = t, auto
	s.updated = time.Now()
	s.mu.Unlock()
	if s.mgr != nil {
		s.mgr.markDirty(s.ID)
	}
}

func (s *Session) setStatus(st string) {
	s.mu.Lock()
	s.status = st
	mgr := s.mgr
	s.mu.Unlock()
	if mgr != nil {
		mgr.markDirty(s.ID)
	}
}

// setModel changes the model used for subsequent prompts.
func (s *Session) setModel(model string) {
	s.mu.Lock()
	s.Model = model
	s.updated = time.Now()
	s.mu.Unlock()
}

func (s *Session) addCost(c float64) {
	s.mu.Lock()
	s.cost += c
	mgr := s.mgr
	s.mu.Unlock()
	if mgr != nil {
		mgr.markDirty(s.ID)
	}
}

// subscribe registers a live channel and returns it plus the current max seq, so
// the caller can replay history up to that seq and then tail without gaps or
// duplicates.
func (s *Session) subscribe() (ch chan Event, historyMax int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch = make(chan Event, 256)
	if s.subs == nil {
		s.subs = map[chan Event]struct{}{}
	}
	s.subs[ch] = struct{}{}
	return ch, s.seq
}

func (s *Session) unsubscribe(ch chan Event) {
	s.mu.Lock()
	delete(s.subs, ch)
	s.mu.Unlock()
}

// history returns events with Seq > after.
func (s *Session) history(after int) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Event
	for _, e := range s.events {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	return out
}

// interrupt cancels a running prompt, if any.
func (s *Session) interrupt() {
	s.mu.Lock()
	c, device, mgr := s.cancel, s.device, s.mgr
	s.mu.Unlock()

	// A device session's run is not here — it is on the linked machine, and
	// cancelling a context this process never created did nothing at all, so
	// Stop left the agent working with no way to reach it.
	if device != "" {
		if mgr != nil && mgr.hub != nil {
			mgr.hub.toDevice(device, relay.Frame{Type: "interrupt", SessionID: s.ID})
		}
		return
	}
	if c != nil {
		c()
	}
}

// Manager owns sessions and builds agents for them.
type Manager struct {
	lead   provider.Provider
	worker provider.Provider // optional
	model  string            // default lead model
	// leadURL is the endpoint lead was configured with. Kept so a session can
	// ask whether its model is loaded before blaming the silence on the run.
	leadURL   string
	workdir   string
	publish   agent.PublishFunc // optional; enables the publish tool
	reviewer  provider.Provider // optional; enables the code-review skill
	revModel  string
	compactor provider.Provider // optional; enables the token-efficiency skill
	compModel string
	compAt    int
	compStats agent.CompactStatsFunc // optional; records compaction repeat-rate
	models    []string               // allowed models to offer clients
	repo      string                 // owner/repo (or dir) label shown on session cards
	hub       *deviceHub             // linked devices (session relay)
	mcp       []agent.MCPServerSpec  // MCP servers whose tools code sessions gain
	// policy is the user's allowed-tools/plugins/permission rules, shared with
	// every other place an agent is built so a session here is gated the same
	// way it would be anywhere else.
	policy []agent.Option

	// pods provisions a workload per session; nil keeps sessions in-process,
	// which is what a Docker or local deployment does.
	pods *PodSessions

	// store persists sessions across restarts; nil disables persistence.
	store   *storage.SessionStore
	dirtyMu sync.Mutex
	dirty   map[string]bool

	mu       sync.Mutex
	sessions map[string]*Session
}

// SetPodSessions runs each new session in its own pod instead of in-process.
func (m *Manager) SetPodSessions(p *PodSessions) { m.pods = p }

// SetStore enables session persistence.
func (m *Manager) SetStore(st *storage.SessionStore) { m.store = st }

// SetModels sets the allowed model list surfaced at /api/models.
func (m *Manager) SetModels(models []string) { m.models = models }

// SetMCP configures the MCP servers whose tools pod-hosted code sessions gain.
func (m *Manager) SetMCP(specs []agent.MCPServerSpec) { m.mcp = specs }

// SetPolicy sets the tool and permission policy every session runs under.
func (m *Manager) SetPolicy(opts []agent.Option) { m.policy = opts }

// SetRepo sets the workspace label (e.g. "owner/repo") shown on session cards.
func (m *Manager) SetRepo(repo string) { m.repo = repo }

// SetPublish enables the agent publish tool, backed by fn.
func (m *Manager) SetPublish(fn agent.PublishFunc) { m.publish = fn }

// SetReviewer enables the code-review skill on served sessions.
func (m *Manager) SetReviewer(p provider.Provider, model string) {
	m.reviewer, m.revModel = p, model
}

// SetCompactor enables the token-efficiency skill on served sessions.
func (m *Manager) SetCompactor(p provider.Provider, model string, threshold int) {
	m.compactor, m.compModel, m.compAt = p, model, threshold
}

// SetCompactStats records compaction observations on served sessions.
func (m *Manager) SetCompactStats(fn agent.CompactStatsFunc) { m.compStats = fn }

// NewManager wires the model backends a served session will use.
func NewManager(lead, worker provider.Provider, defaultModel, workdir string) *Manager {
	return &Manager{
		lead: lead, worker: worker, model: defaultModel, workdir: workdir,
		sessions: map[string]*Session{},
	}
}

// CreateReq is the body of POST /sessions.
type CreateReq struct {
	Model       string `json:"model"`
	WorkerModel string `json:"worker_model"`
	Style       string `json:"style"`
	Chat        bool   `json:"chat"` // chat session: web/context only, no file or shell tools
}

func (m *Manager) create(req CreateReq, owner string) *Session {
	model := req.Model
	if model == "" {
		model = m.model
	}
	id := "ses_" + randomID()
	// Each session gets its own workspace under the base workdir. This scopes
	// the file tools (workspace.resolve refuses to leave the session root) but
	// it is NOT isolation: bash gets a cwd, not a jail, so `cd ..` still reads a
	// sibling session's files. Every session runs as the same uid, so unix
	// permissions cannot separate them either — real isolation needs a mount
	// namespace or a container per session, which the pod cannot create now that
	// it runs unprivileged with capabilities dropped. Acceptable while the
	// registered users trust each other; revisit before opening signup.
	workdir := m.workdir
	if m.workdir != "" {
		d := filepath.Join(m.workdir, id)
		if err := os.MkdirAll(d, 0o700); err == nil {
			workdir = d
		}
	}
	s := &Session{
		ID:          id,
		Model:       model,
		Provider:    m.lead.Name(),
		WorkerModel: req.WorkerModel,
		Style:       req.Style,
		Chat:        req.Chat,
		owner:       owner,
		mgr:         m,
		workdir:     workdir,
		repo:        m.repo,
		updated:     time.Now(),
		status:      "idle",
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	// Persist immediately: a session with no events yet is still a session, and
	// only marking on emit would lose one created just before a restart.
	m.markDirty(s.ID)
	return s
}

// get returns the session only if owner may see it. An unowned session (created
// before ownership existed, or by an unauthenticated server) is visible to all,
// so enabling auth cannot strand existing work.
func (m *Manager) get(id, owner string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || !s.visibleTo(owner) {
		return nil, false
	}
	return s, true
}

// getAny looks a session up without an ownership check. Only for internal
// plumbing (the device relay), never for a request handler.
func (m *Manager) getAny(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

// visibleTo reports whether owner may access this session.
func (s *Session) visibleTo(owner string) bool {
	return s.owner == "" || owner == "" || strings.EqualFold(s.owner, owner)
}

// remove cancels any running prompt and drops the session from the manager.
// Returns false if the id was unknown.
func (m *Manager) remove(id, owner string) bool {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok && !s.visibleTo(owner) {
		m.mu.Unlock()
		return false // indistinguishable from "unknown id"
	}
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if ok {
		m.forget(id)  // don't let a restart resurrect a deleted session
		s.interrupt() // stop any in-flight run
		// Clean up the session's sandbox dir (only when it's a per-session
		// subdir under the base workdir, never the base itself).
		if m.workdir != "" && s.workdir != "" && s.workdir != m.workdir &&
			strings.HasPrefix(s.workdir, m.workdir+string(filepath.Separator)) {
			_ = os.RemoveAll(s.workdir)
		}
	}
	return ok
}

func (m *Manager) list(owner string) []sessionView {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]sessionView, 0, len(m.sessions))
	for _, s := range m.sessions {
		if !s.visibleTo(owner) {
			continue
		}
		out = append(out, s.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// prompt starts a run in the background. It returns immediately; the run
// continues server-side and streams events into the session.
func (m *Manager) prompt(s *Session, prompt string) error {
	s.mu.Lock()
	if s.status == "running" {
		s.mu.Unlock()
		return fmt.Errorf("session is already running")
	}
	// Device sessions run on the linked laptop: relay the prompt over the hub
	// and let its events stream back. The pod runs no agent for these.
	if s.device != "" {
		if s.title == "" || s.titleAuto {
			s.title, s.titleAuto = truncate(oneLine(prompt), 60), false
		}
		s.status = "running"
		s.picked = false
		s.mu.Unlock()
		m.markBusy(s.ID, true)
		s.failIfNotPickedUp()
		if m.hub == nil || !m.hub.toDevice(s.device, relay.Frame{Type: "prompt", SessionID: s.ID, Text: prompt}) {
			s.setStatus("idle")
			return fmt.Errorf("device %q is not connected", s.device)
		}
		s.emit("prompt", prompt)
		s.warnIfSilent()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.status = "running"
	if s.title == "" || s.titleAuto {
		s.title, s.titleAuto = truncate(oneLine(prompt), 60), false
	}
	model := s.Model
	chat := s.Chat
	s.mu.Unlock()

	opts := []agent.Option{agent.WithEventHook(func(e agent.Event) { s.emit(e.Kind, e.Data) })}
	if chat {
		// Chat sessions have no filesystem or shell: they answer from the prompt,
		// provided context, and the web. Restrict to the web tools.
		// Chat gets retrieval and nothing else — no shell, no filesystem. The
		// exa tools are the same capability from a service built for it: better
		// results than scraping a search page, and the only way a local model
		// can cite anything real. Named explicitly rather than allowing every
		// MCP tool, because "chat has no tools but these" is the property worth
		// keeping.
		chatTools := []string{"web_fetch", "web_search"}
		for _, spec := range m.mcp {
			if spec.Name == "exa" {
				chatTools = append(chatTools,
					"web_search_exa", "web_fetch_exa", "web_search_advanced_exa")
			}
		}
		opts = append(opts, agent.WithAllowedTools(chatTools))
	}
	if !chat && m.worker != nil && s.WorkerModel != "" {
		opts = append(opts, agent.WithWorker(m.worker, s.WorkerModel))
	}
	if !chat && len(m.mcp) > 0 {
		// Code sessions gain the configured MCP servers' tools. Chat sessions are
		// restricted to web tools, so MCP tools would be blocked anyway.
		opts = append(opts, agent.WithMCPServers(m.mcp))
	}
	if m.publish != nil {
		opts = append(opts, agent.WithPublish(m.publish))
	}
	if m.reviewer != nil && m.revModel != "" {
		opts = append(opts, agent.WithReviewer(m.reviewer, m.revModel))
	}
	if m.compactor != nil && m.compModel != "" {
		opts = append(opts, agent.WithCompactor(m.compactor, m.compModel, m.compAt))
		if m.compStats != nil {
			opts = append(opts, agent.WithCompactStats(m.compStats))
		}
	}
	opts = append(opts, m.policy...)
	identity := config.DefaultIdentity
	if chat {
		identity = config.DefaultChatIdentity
	}
	ag, err := agent.New(agent.Config{
		Name: "dross", Model: model, Workdir: s.workdir, Style: agent.Style(s.Style),
		Chat:     chat,
		Identity: identity,
		// Pod-hosted Code sessions are sandboxed: tool installs stay in the
		// per-session workspace. Chat sessions have no shell, so no sandbox is
		// needed. Device (remote-control) sessions never reach here — they relay
		// to the linked machine and use its real environment.
		Sandbox: !chat,
	}, m.lead, opts...)
	if err != nil {
		return err
	}

	s.emit("prompt", prompt)
	m.sayIfModelIsCold(s)
	s.warnIfSilent()
	go func() {
		defer ag.Close() // release MCP connections when the turn ends
		res, err := ag.Run(ctx, prompt)
		if err != nil {
			// Interrupting cancels the run's context. That's the user pressing
			// Stop, not a failure, so close the turn normally rather than
			// surfacing "context canceled" as an error.
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				if res != nil {
					s.addCost(res.Cost())
				}
				s.setStatus("idle")
				s.emit("done", "cancelled")
				return
			}
			// Set status before emitting so a client that re-fetches on the event
			// sees the final state, not a stale "running".
			s.setStatus("error")
			s.emit("error", err.Error())
			return
		}
		s.addCost(res.Cost())
		s.setStatus("idle")
		s.emit("done", fmt.Sprintf("$%.4f · %d turns", res.Cost(), res.Turns))
	}()
	return nil
}

// oneLine collapses a string to its first line, trimmed.
func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// truncate shortens s to at most n runes, appending an ellipsis when cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
