package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dougkirkley/dross/internal/forge"
)

func store(t *testing.T) (*ForgeTokens, string) {
	t.Helper()
	dir := t.TempDir()
	f, err := NewForgeTokens(dir, []byte("a cookie secret"))
	if err != nil {
		t.Fatal(err)
	}
	return f, filepath.Join(dir, "forge")
}

func TestSaveAndLoad(t *testing.T) {
	f, _ := store(t)
	want := forge.Token{Access: "gho_abc", Refresh: "ghr_def", Expiry: time.Now().Add(time.Hour).Round(time.Second)}
	if err := f.Save("doug@example.com", "github", want); err != nil {
		t.Fatal(err)
	}
	got, ok := f.Load("doug@example.com", "github")
	if !ok {
		t.Fatal("token did not come back")
	}
	if got.Access != want.Access || got.Refresh != want.Refresh || !got.Expiry.Equal(want.Expiry) {
		t.Errorf("got %+v; want %+v", got, want)
	}
}

// The whole reason this file is not plain JSON. The volume outlives the pod, is
// snapshotted, and is readable by anything that can mount it — and the token is
// a key to somebody's source on a system this server does not own.
func TestTokenIsNotOnDiskInTheClear(t *testing.T) {
	f, dir := store(t)
	if err := f.Save("doug@example.com", "github", forge.Token{Access: "gho_secret_value"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "gho_secret_value") {
		t.Error("the access token is readable on disk")
	}
}

// The filename must not be a list of everyone's email address.
func TestFilenameDoesNotLeakTheIdentity(t *testing.T) {
	f, dir := store(t)
	if err := f.Save("doug.kirkley@gmail.com", "github", forge.Token{Access: "a"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "doug") || strings.Contains(e.Name(), "@") ||
			strings.Contains(e.Name(), "gmail") {
			t.Errorf("filename %q carries the identity", e.Name())
		}
	}
}

// Two users on one host, and one user on two hosts, must not collide.
func TestCredentialsAreKeyedByBothUserAndHost(t *testing.T) {
	f, _ := store(t)
	_ = f.Save("a@example.com", "github", forge.Token{Access: "a-github"})
	_ = f.Save("b@example.com", "github", forge.Token{Access: "b-github"})
	_ = f.Save("a@example.com", "gitlab", forge.Token{Access: "a-gitlab"})

	for _, c := range []struct{ owner, host, want string }{
		{"a@example.com", "github", "a-github"},
		{"b@example.com", "github", "b-github"},
		{"a@example.com", "gitlab", "a-gitlab"},
	} {
		got, ok := f.Load(c.owner, c.host)
		if !ok || got.Access != c.want {
			t.Errorf("%s/%s = %q,%v; want %q", c.owner, c.host, got.Access, ok, c.want)
		}
	}
	if _, ok := f.Load("c@example.com", "github"); ok {
		t.Error("a user who never connected has a credential")
	}
}

// A store written under a different cookie secret cannot be read, and that has
// to read as "not connected" — something the user can fix by connecting again —
// rather than as an error they can do nothing about.
func TestUndecryptableCredentialReadsAsNotConnected(t *testing.T) {
	dir := t.TempDir()
	first, _ := NewForgeTokens(dir, []byte("the old secret"))
	if err := first.Save("doug@example.com", "github", forge.Token{Access: "a"}); err != nil {
		t.Fatal(err)
	}
	second, err := NewForgeTokens(dir, []byte("a different secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := second.Load("doug@example.com", "github"); ok {
		t.Error("a credential decrypted under the wrong key")
	}
}

func TestDeleteForgets(t *testing.T) {
	f, _ := store(t)
	_ = f.Save("doug@example.com", "github", forge.Token{Access: "a"})
	f.Delete("doug@example.com", "github")
	if _, ok := f.Load("doug@example.com", "github"); ok {
		t.Error("the credential survived a disconnect")
	}
}

// A server with no SSO has no identities to key credentials on. It must still
// build and start, and every call has to be safe on the nil store.
func TestNilStoreIsUsable(t *testing.T) {
	f, err := NewForgeTokens(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if f != nil {
		t.Fatal("a store was built with no secret")
	}
	if _, ok := f.Load("a", "github"); ok {
		t.Error("the nil store returned a credential")
	}
	if err := f.Save("a", "github", forge.Token{Access: "x"}); err == nil {
		t.Error("the nil store accepted a credential")
	}
	f.Delete("a", "github")
	if got := f.Hosts("a"); got != nil {
		t.Errorf("Hosts on the nil store = %v", got)
	}
}

// Saving twice replaces rather than accumulating: a reconnect must not leave
// the previous token readable.
func TestSaveReplaces(t *testing.T) {
	f, dir := store(t)
	_ = f.Save("doug@example.com", "github", forge.Token{Access: "first"})
	_ = f.Save("doug@example.com", "github", forge.Token{Access: "second"})

	got, _ := f.Load("doug@example.com", "github")
	if got.Access != "second" {
		t.Errorf("access = %q after reconnecting", got.Access)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("%d files for one user and one host", len(entries))
	}
}
