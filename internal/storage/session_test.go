package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadList(t *testing.T) {
	dir := t.TempDir()
	s := NewSessionStore(dir)

	if err := s.SaveSession("a", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadSession("a")
	if err != nil || string(got) != "hello" {
		t.Fatalf("load: %q %v", got, err)
	}

	// File written with the .json suffix.
	if _, err := os.Stat(filepath.Join(dir, "a.json")); err != nil {
		t.Fatalf("expected a.json: %v", err)
	}

	s.SaveSession("b", []byte("x"))
	names, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Fatalf("list = %v", names)
	}

	// Missing session load errors with IsNotExist.
	if _, err := s.LoadSession("missing"); !os.IsNotExist(err) {
		t.Fatalf("missing load err = %v", err)
	}
}

// TestNameSanitized ensures a name with separators can't escape the base dir.
func TestNameSanitized(t *testing.T) {
	dir := t.TempDir()
	s := NewSessionStore(dir)
	if err := s.SaveSession("../../etc/passwd", []byte("x")); err != nil {
		t.Fatal(err)
	}
	// Written as passwd.json inside dir, nothing outside.
	if _, err := os.Stat(filepath.Join(dir, "passwd.json")); err != nil {
		t.Fatalf("expected sanitized passwd.json in dir: %v", err)
	}
}

// TestList_EmptyDir returns nil without error when the dir has no sessions.
func TestList_EmptyDir(t *testing.T) {
	s := NewSessionStore(t.TempDir())
	names, err := s.List()
	if err != nil || len(names) != 0 {
		t.Fatalf("empty list = %v %v", names, err)
	}
}
