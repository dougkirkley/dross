package server

import (
	"context"
	"encoding/json"
	"os"
	"time"
)

// Session persistence. Sessions are the user's work — prompt history, agent
// output, cost — and losing them on every deploy makes the server feel
// disposable. The manager keeps them in memory for speed and mirrors each
// change to disk asynchronously.
//
// Writes are debounced rather than synchronous: emit() runs on the hot path of
// a streaming run, and rewriting a session's whole history per event would turn
// a long run into O(n^2) disk traffic.

// persisted is the on-disk form of a Session. It is a separate type from
// Session because the live struct holds a mutex, subscribers and a cancel func,
// none of which survive a restart.
type persisted struct {
	ID          string    `json:"id"`
	Model       string    `json:"model"`
	Provider    string    `json:"provider"`
	WorkerModel string    `json:"worker_model,omitempty"`
	Style       string    `json:"style,omitempty"`
	Chat        bool      `json:"chat,omitempty"`
	Owner       string    `json:"owner,omitempty"`
	Workdir     string    `json:"workdir,omitempty"`
	Repo        string    `json:"repo,omitempty"`
	Title       string    `json:"title,omitempty"`
	Last        string    `json:"last,omitempty"`
	Updated     time.Time `json:"updated"`
	Cost        float64   `json:"cost"`
	Seq         int       `json:"seq"`
	Events      []Event   `json:"events,omitempty"`
	Device      string    `json:"device,omitempty"`
	// Setup is what a session is still waiting to be able to report — a clone
	// that a pod is doing on its way up. In memory only, it was lost whenever
	// the server restarted inside that window, and the transcript then said
	// "cloning" for ever because the moment that would have finished it had
	// already gone by.
	Setup string `json:"setup,omitempty"`
	// SetupDoing is the present-tense half, so a restart mid-clone comes back
	// still saying what it is waiting on rather than saying nothing.
	SetupDoing string `json:"setup_doing,omitempty"`
}

// snapshotForDisk copies the fields worth keeping, under the session lock.
func (s *Session) snapshotForDisk() persisted {
	s.mu.Lock()
	defer s.mu.Unlock()
	return persisted{
		ID: s.ID, Model: s.Model, Provider: s.Provider,
		WorkerModel: s.WorkerModel, Style: s.Style, Chat: s.Chat,
		Owner: s.owner, Workdir: s.workdir, Repo: s.repo,
		Title: s.title, Last: s.last, Updated: s.updated,
		Cost: s.cost, Seq: s.seq,
		Events: append([]Event(nil), s.events...),
		Device: s.device, Setup: s.setup, SetupDoing: s.setupDoing,
	}
}

// markDirty flags a session for the next flush.
func (m *Manager) markDirty(id string) {
	if m.store == nil {
		return
	}
	m.dirtyMu.Lock()
	if m.dirty == nil {
		m.dirty = map[string]bool{}
	}
	m.dirty[id] = true
	m.dirtyMu.Unlock()
}

// flush writes every session that changed since the last pass.
func (m *Manager) flush() {
	if m.store == nil {
		return
	}
	m.dirtyMu.Lock()
	ids := make([]string, 0, len(m.dirty))
	for id := range m.dirty {
		ids = append(ids, id)
	}
	m.dirty = map[string]bool{}
	m.dirtyMu.Unlock()

	for _, id := range ids {
		s, ok := m.getAny(id)
		if !ok {
			continue // deleted between the mark and the flush
		}
		if b, err := json.Marshal(s.snapshotForDisk()); err == nil {
			_ = m.store.SaveSession(id, b)
		}
	}
}

// Persist mirrors session changes to disk until ctx is cancelled, then makes a
// final pass so a clean shutdown loses nothing.
func (m *Manager) Persist(ctx context.Context, every time.Duration) {
	if m.store == nil {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.flush()
			return
		case <-t.C:
			m.flush()
		}
	}
}

// Restore loads previously saved sessions. A session that was mid-run when the
// process died is marked idle: the run is gone, and leaving it "running" would
// show a spinner that never resolves and refuse new prompts.
func (m *Manager) Restore() (int, error) {
	if m.store == nil {
		return 0, nil
	}
	names, err := m.store.List()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, name := range names {
		b, err := m.store.LoadSession(name)
		if err != nil {
			continue
		}
		var p persisted
		if err := json.Unmarshal(b, &p); err != nil || p.ID == "" {
			continue
		}
		s := &Session{
			ID: p.ID, Model: p.Model, Provider: p.Provider,
			WorkerModel: p.WorkerModel, Style: p.Style, Chat: p.Chat,
			owner: p.Owner, workdir: p.Workdir, repo: p.Repo, setup: p.Setup, setupDoing: p.SetupDoing,
			title: p.Title, last: p.Last, updated: p.Updated,
			status: "idle", cost: p.Cost, seq: p.Seq, events: p.Events,
			device: p.Device,
			mgr:    m,
		}
		// The workspace should still be there, but a session must not come back
		// broken if the volume was replaced — recreate the directory so tools
		// have somewhere to work.
		if s.workdir != "" {
			_ = os.MkdirAll(s.workdir, 0o755)
		}
		m.mu.Lock()
		m.sessions[s.ID] = s
		m.mu.Unlock()
		n++
	}
	return n, nil
}

// Forget drops a session's saved copy, so a delete is not resurrected by the
// next restart.
func (m *Manager) forget(id string) {
	if m.store == nil {
		return
	}
	m.dirtyMu.Lock()
	delete(m.dirty, id)
	m.dirtyMu.Unlock()
	_ = m.store.DeleteSession(id)
}
