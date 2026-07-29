// Package session persists a compact "handoff brief" per named session — the
// crucial state a fresh session needs to resume, rather than a full transcript.
// This is the cheap alternative to transcript compaction: keep each session
// short and hand off only what matters (see DESIGN.md).
package session

import (
	"encoding/json"
	"os"
	"time"

	"github.com/dougkirkley/ergon/internal/storage"
)

// Brief is the durable, resumable summary of a session.
type Brief struct {
	Name    string    `json:"name"`
	Updated time.Time `json:"updated"`
	Content string    `json:"content"` // markdown handoff written by the agent
}

// Save writes/overwrites the brief for name.
func Save(store *storage.SessionStore, name, content string) error {
	data, err := json.MarshalIndent(Brief{
		Name:    name,
		Updated: time.Now(),
		Content: content,
	}, "", "  ")
	if err != nil {
		return err
	}
	return store.SaveSession(name, data)
}

// Load returns the brief for name. found is false (with nil error) when no brief
// has been saved yet.
func Load(store *storage.SessionStore, name string) (b *Brief, found bool, err error) {
	data, err := store.LoadSession(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var out Brief
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, false, err
	}
	return &out, true, nil
}

// List returns saved session names.
func List(store *storage.SessionStore) ([]string, error) {
	return store.List()
}
