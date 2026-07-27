// Package artifact stores files and serves them at unguessable capability URLs
// (/a/<id>) — the "share this screenshot/gif/report" path. The random id is the
// only credential, so a link is shareable with people who lack an account, and
// must only ever expose a published blob's bytes.
package artifact

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Store persists published blobs plus a sidecar of metadata on disk.
type Store struct {
	dir     string
	baseURL string // external origin for links; empty → relative "/a/<id>"
}

// NewStore creates (if needed) the artifact directory.
func NewStore(dir, baseURL string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir, baseURL: baseURL}, nil
}

// Meta describes a stored artifact.
type Meta struct {
	ID       string    `json:"id"`
	Mime     string    `json:"mime"`
	Filename string    `json:"filename"`
	Created  time.Time `json:"created"`
}

// Put writes bytes under a fresh random id and returns its metadata.
func (s *Store) Put(data []byte, mime, filename string) (Meta, error) {
	if mime == "" {
		mime = "application/octet-stream"
	}
	m := Meta{ID: randomID(), Mime: mime, Filename: filepath.Base(filename), Created: time.Now().UTC()}
	if err := os.WriteFile(s.blobPath(m.ID), data, 0o644); err != nil {
		return m, err
	}
	meta, _ := json.Marshal(m)
	if err := os.WriteFile(s.metaPath(m.ID), meta, 0o644); err != nil {
		return m, err
	}
	return m, nil
}

// Get returns the bytes and metadata for id.
func (s *Store) Get(id string) ([]byte, Meta, error) {
	var m Meta
	if !ValidID(id) {
		return nil, m, fmt.Errorf("invalid id")
	}
	metaBytes, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		return nil, m, err
	}
	if err := json.Unmarshal(metaBytes, &m); err != nil {
		return nil, m, err
	}
	data, err := os.ReadFile(s.blobPath(id))
	return data, m, err
}

// URL returns the shareable link for id (absolute if a base URL is set).
func (s *Store) URL(id string) string {
	if s.baseURL != "" {
		return s.baseURL + "/a/" + id
	}
	return "/a/" + id
}

// id is validated hex, so it can never traverse out of the artifact dir.
func (s *Store) blobPath(id string) string { return filepath.Join(s.dir, id+".blob") }
func (s *Store) metaPath(id string) string { return filepath.Join(s.dir, id+".json") }

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ValidID reports whether id is a 32-char hex string (a minted artifact id).
func ValidID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
