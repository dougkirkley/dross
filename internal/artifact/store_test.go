package artifact

import (
	"strings"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	s, err := NewStore(t.TempDir(), "https://holocron.example.com")
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Put([]byte("hello"), "image/png", "dir/shot.png")
	if err != nil {
		t.Fatal(err)
	}
	if !ValidID(m.ID) || m.Mime != "image/png" || m.Filename != "shot.png" {
		t.Fatalf("meta = %+v", m)
	}
	if s.URL(m.ID) != "https://holocron.example.com/a/"+m.ID {
		t.Fatalf("url = %s", s.URL(m.ID))
	}
	data, got, err := s.Get(m.ID)
	if err != nil || string(data) != "hello" || got.Mime != "image/png" {
		t.Fatalf("get: %q %+v %v", data, got, err)
	}
}

func TestStore_RelativeURL(t *testing.T) {
	s, _ := NewStore(t.TempDir(), "")
	m, _ := s.Put([]byte("x"), "text/plain", "a.txt")
	if u := s.URL(m.ID); u != "/a/"+m.ID {
		t.Fatalf("relative url = %s", u)
	}
}

func TestStore_MimeDefault(t *testing.T) {
	s, _ := NewStore(t.TempDir(), "")
	m, _ := s.Put([]byte("x"), "", "a")
	if m.Mime != "application/octet-stream" {
		t.Fatalf("mime = %s", m.Mime)
	}
}

func TestValidID(t *testing.T) {
	s, _ := NewStore(t.TempDir(), "")
	m, _ := s.Put([]byte("x"), "text/plain", "a")
	if !ValidID(m.ID) {
		t.Fatal("minted id should be valid")
	}
	for _, bad := range []string{"", "xyz", "../../etc/passwd", strings.Repeat("g", 32)} {
		if ValidID(bad) {
			t.Fatalf("ValidID accepted %q", bad)
		}
		if _, _, err := s.Get(bad); err == nil {
			t.Fatalf("Get accepted bad id %q", bad)
		}
	}
}
