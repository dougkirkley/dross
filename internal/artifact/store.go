// Package artifact stores published files (screenshots, gifs, reports).
//
// Artifacts are PRIVATE by default: /a/<id> sits behind the server's normal
// auth, so only a signed-in user can view one. Sharing is explicit — minting a
// share token for an artifact publishes it at /s/<id>/<token>, which needs no
// account, and revoking the token kills that link immediately. The id alone is
// never sufficient to read a blob from the public endpoint.
package artifact

import (
	"crypto/rand"
	"crypto/subtle"
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

// Meta describes a stored artifact. Share is the public-link token: empty means
// the artifact is private and only reachable through the authenticated route.
type Meta struct {
	ID       string    `json:"id"`
	Mime     string    `json:"mime"`
	Filename string    `json:"filename"`
	Created  time.Time `json:"created"`
	Share    string    `json:"share,omitempty"`
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

// URL returns the private link for id — viewable only while signed in
// (absolute if a base URL is set).
func (s *Store) URL(id string) string { return s.link("/a/" + id) }

// ShareURL returns the public link for an artifact that has been shared.
func (s *Store) ShareURL(m Meta) string {
	if m.Share == "" {
		return ""
	}
	return s.link("/s/" + m.ID + "/" + m.Share)
}

func (s *Store) link(path string) string {
	if s.baseURL != "" {
		return s.baseURL + path
	}
	return path
}

// meta reads an artifact's sidecar.
func (s *Store) meta(id string) (Meta, error) {
	var m Meta
	if !ValidID(id) {
		return m, fmt.Errorf("invalid id")
	}
	b, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

// Share mints (or returns the existing) public token for id, making it readable
// without an account at /s/<id>/<token>.
func (s *Store) Share(id string) (Meta, error) {
	m, err := s.meta(id)
	if err != nil {
		return m, err
	}
	if m.Share == "" {
		m.Share = randomID()
		b, _ := json.Marshal(m)
		if err := os.WriteFile(s.metaPath(id), b, 0o644); err != nil {
			return m, err
		}
	}
	return m, nil
}

// Unshare revokes the public token, so the shared link stops resolving. The
// artifact itself stays available to signed-in users.
func (s *Store) Unshare(id string) error {
	m, err := s.meta(id)
	if err != nil {
		return err
	}
	m.Share = ""
	b, _ := json.Marshal(m)
	return os.WriteFile(s.metaPath(id), b, 0o644)
}

// GetShared returns the blob only if id has been shared and token matches.
// The comparison is constant time so the token can't be probed byte by byte.
func (s *Store) GetShared(id, token string) ([]byte, Meta, error) {
	m, err := s.meta(id)
	if err != nil {
		return nil, m, err
	}
	if m.Share == "" || subtle.ConstantTimeCompare([]byte(m.Share), []byte(token)) != 1 {
		return nil, m, fmt.Errorf("not shared")
	}
	data, err := os.ReadFile(s.blobPath(id))
	return data, m, err
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
