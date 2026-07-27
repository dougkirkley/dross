package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/dougkirkley/holocron/internal/agent"
	"github.com/dougkirkley/holocron/internal/provider"
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

	mu      sync.Mutex
	workdir string
	status  string // "idle" | "running" | "error"
	cost    float64
	seq     int
	events  []Event
	subs    map[chan Event]struct{}
	cancel  context.CancelFunc
}

// Status/Cost snapshots (locked reads for the JSON views).
func (s *Session) snapshot() sessionView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sessionView{
		ID: s.ID, Model: s.Model, Provider: s.Provider,
		WorkerModel: s.WorkerModel, Style: s.Style,
		Status: s.status, Cost: s.cost, Events: len(s.events),
	}
}

type sessionView struct {
	ID          string  `json:"id"`
	Model       string  `json:"model"`
	Provider    string  `json:"provider"`
	WorkerModel string  `json:"worker_model,omitempty"`
	Style       string  `json:"style,omitempty"`
	Status      string  `json:"status"`
	Cost        float64 `json:"cost"`
	Events      int     `json:"events"`
}

// emit appends an event and fans it out to live subscribers.
func (s *Session) emit(kind, data string) {
	s.mu.Lock()
	s.seq++
	e := Event{Seq: s.seq, Kind: kind, Data: data, Time: time.Now()}
	s.events = append(s.events, e)
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
	lead    provider.Provider
	worker  provider.Provider // optional
	model   string            // default lead model
	workdir string
	publish agent.PublishFunc // optional; enables the publish tool

	mu       sync.Mutex
	sessions map[string]*Session
}

// SetPublish enables the agent publish tool, backed by fn.
func (m *Manager) SetPublish(fn agent.PublishFunc) { m.publish = fn }

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
	s := &Session{
		ID:          "ses_" + randomID(),
		Model:       model,
		Provider:    m.lead.Name(),
		WorkerModel: req.WorkerModel,
		Style:       req.Style,
		workdir:     m.workdir,
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
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.status = "running"
	s.mu.Unlock()

	opts := []agent.Option{agent.WithEventHook(func(e agent.Event) { s.emit(e.Kind, e.Data) })}
	if m.worker != nil && s.WorkerModel != "" {
		opts = append(opts, agent.WithWorker(m.worker, s.WorkerModel))
	}
	if m.publish != nil {
		opts = append(opts, agent.WithPublish(m.publish))
	}
	ag, err := agent.New(agent.Config{
		Name: "holocron", Model: s.Model, Workdir: s.workdir, Style: agent.Style(s.Style),
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

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
