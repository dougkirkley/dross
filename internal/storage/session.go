package storage

import (
	"os"
	"path/filepath"
	"strings"
)

type SessionStore struct {
	basePath string
}

func NewSessionStore(path string) *SessionStore {
	return &SessionStore{
		basePath: path,
	}
}

func (s *SessionStore) path(name string) string {
	// filepath.Base defends against a name with path separators.
	return filepath.Join(s.basePath, filepath.Base(name)+".json")
}

func (s *SessionStore) SaveSession(name string, data []byte) error {
	return os.WriteFile(s.path(name), data, 0o600)
}

func (s *SessionStore) LoadSession(name string) ([]byte, error) {
	return os.ReadFile(s.path(name))
}

// DeleteSession removes a stored session. A missing file is not an error: the
// caller's intent (it should not exist) is already satisfied.
func (s *SessionStore) DeleteSession(name string) error {
	err := os.Remove(s.path(name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// List returns the names of all stored sessions (without the .json suffix).
func (s *SessionStore) List() ([]string, error) {
	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if n := strings.TrimSuffix(e.Name(), ".json"); n != e.Name() {
			names = append(names, n)
		}
	}
	return names, nil
}
