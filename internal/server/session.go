package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dougkirkley/holocron/internal/agent"
	"github.com/dougkirkley/holocron/internal/provider"
	"github.com/dougkirkley/holocron/internal/relay"
)

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

	mu        sync.Mutex
	workdir   string
	repo      string // owner/repo (or dir name) label for the session card
	title     string // first prompt, truncated — the session's display name
	last      string // most recent user/agent message, for the list preview
	updated   time.Time
	status    string // "idle" | "running" | "error"
	cost      float64
	seq       int
	events    []Event
	subs      map[chan Event]struct{}
	cancel    context.CancelFunc
	device    string // owning device id ("" = runs in this pod)
	connected bool   // for device sessions: is that device currently linked
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
func (s *Session) emit(kind, data string) {
	s.mu.Lock()
	s.seq++
	e := Event{Seq: s.seq, Kind: kind, Data: data, Time: time.Now()}
	s.events = append(s.events, e)
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
	s.mu.Unlock()
}

func (s *Session) setStatus(st string) {
	s.mu.Lock()
	s.status = st
	s.mu.Unlock()
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
	s.mu.Unlock()
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
	c := s.cancel
	s.mu.Unlock()
	if c != nil {
		c()
	}
}

// Manager owns sessions and builds agents for them.
type Manager struct {
	lead      provider.Provider
	worker    provider.Provider // optional
	model     string            // default lead model
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

	mu       sync.Mutex
	sessions map[string]*Session
}

// SetModels sets the allowed model list surfaced at /api/models.
func (m *Manager) SetModels(models []string) { m.models = models }

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
}

func (m *Manager) create(req CreateReq) *Session {
	model := req.Model
	if model == "" {
		model = m.model
	}
	id := "ses_" + randomID()
	// Each session gets its own sandbox workspace under the base workdir, so
	// sessions can't see or clobber each other's files.
	workdir := m.workdir
	if m.workdir != "" {
		d := filepath.Join(m.workdir, id)
		if err := os.MkdirAll(d, 0o755); err == nil {
			workdir = d
		}
	}
	s := &Session{
		ID:          id,
		Model:       model,
		Provider:    m.lead.Name(),
		WorkerModel: req.WorkerModel,
		Style:       req.Style,
		workdir:     workdir,
		repo:        m.repo,
		updated:     time.Now(),
		status:      "idle",
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	return s
}

func (m *Manager) get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

// remove cancels any running prompt and drops the session from the manager.
// Returns false if the id was unknown.
func (m *Manager) remove(id string) bool {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if ok {
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

func (m *Manager) list() []sessionView {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]sessionView, 0, len(m.sessions))
	for _, s := range m.sessions {
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
		if s.title == "" {
			s.title = truncate(oneLine(prompt), 60)
		}
		s.status = "running"
		s.mu.Unlock()
		if m.hub == nil || !m.hub.toDevice(s.device, relay.Frame{Type: "prompt", SessionID: s.ID, Text: prompt}) {
			s.setStatus("idle")
			return fmt.Errorf("device %q is not connected", s.device)
		}
		s.emit("prompt", prompt)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.status = "running"
	if s.title == "" {
		s.title = truncate(oneLine(prompt), 60)
	}
	model := s.Model
	s.mu.Unlock()

	opts := []agent.Option{agent.WithEventHook(func(e agent.Event) { s.emit(e.Kind, e.Data) })}
	if m.worker != nil && s.WorkerModel != "" {
		opts = append(opts, agent.WithWorker(m.worker, s.WorkerModel))
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
	ag, err := agent.New(agent.Config{
		Name: "holocron", Model: model, Workdir: s.workdir, Style: agent.Style(s.Style),
	}, m.lead, opts...)
	if err != nil {
		return err
	}

	s.emit("prompt", prompt)
	go func() {
		res, err := ag.Run(ctx, prompt)
		if err != nil {
			s.emit("error", err.Error())
			s.setStatus("error")
			return
		}
		s.addCost(res.Cost())
		s.emit("done", fmt.Sprintf("$%.4f · %d turns", res.Cost(), res.Turns))
		s.setStatus("idle")
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
