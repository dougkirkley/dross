package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dougkirkley/dross/internal/forge"
)

// ForgeTokens stores each user's git-host credentials.
//
// Encrypted on disk, unlike the session transcripts beside them. A session's
// events are this server's own data; a forge token is a key to somebody's
// source code on a system this server does not own, and it stays useful to
// whoever takes it for as long as the user leaves it authorized. The volume
// holding it is a PVC that outlives the pod, gets snapshotted, and is readable
// by anything that can mount it.
//
// The key is derived from the cookie secret rather than being one more thing to
// manage. That secret already protects every session in the deployment, so a
// separate key would add a second thing to lose without raising the floor:
// anyone holding the cookie secret can already mint an identity and ask this
// server for a fresh token through the normal flow.
type ForgeTokens struct {
	dir string
	key []byte
	// state signs the cookie that carries a pending authorization through the
	// host and back. Its own key: that cookie is handed to a browser where the
	// tokens never are, and one key doing both jobs would mean an oracle for
	// the other on any day the signing turns out to be the weaker half.
	state []byte

	mu sync.Mutex
}

// NewForgeTokens opens the store under dir, deriving its key from secret.
//
// A nil store — no secret — is usable and refuses to save, so a server without
// SSO can still be built and started. It has no identities to key credentials
// on in the first place.
func NewForgeTokens(dir string, secret []byte) (*ForgeTokens, error) {
	if len(secret) == 0 {
		return nil, nil
	}
	p := filepath.Join(dir, "forge")
	if err := os.MkdirAll(p, 0o700); err != nil {
		return nil, err
	}
	// Derived, not used directly: the cookie secret is an HMAC key elsewhere,
	// and using one key for two algorithms is the kind of reuse that is fine
	// until the day it is not.
	sum := sha256.Sum256(append([]byte("dross/forge-tokens\x00"), secret...))
	st := sha256.Sum256(append([]byte("dross/forge-state\x00"), secret...))
	return &ForgeTokens{dir: p, key: sum[:], state: st[:]}, nil
}

// Save records a user's token for one host, replacing any it already had.
func (f *ForgeTokens) Save(owner, host string, tok forge.Token) error {
	if f == nil {
		return fmt.Errorf("no credential store configured")
	}
	b, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	sealed, err := f.seal(b)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Written beside and renamed, so a crash mid-write leaves the previous
	// credential rather than a truncated file that decrypts to nothing.
	name := f.path(owner, host)
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, sealed, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, name)
}

// Load returns a user's token for one host.
func (f *ForgeTokens) Load(owner, host string) (forge.Token, bool) {
	if f == nil {
		return forge.Token{}, false
	}
	f.mu.Lock()
	b, err := os.ReadFile(f.path(owner, host))
	f.mu.Unlock()
	if err != nil {
		return forge.Token{}, false
	}
	plain, err := f.open(b)
	if err != nil {
		// A file that will not decrypt is a file written under a different
		// cookie secret. Nothing can be done with it and the user can simply
		// connect again, so it reads as "not connected" rather than as an error
		// they cannot act on.
		return forge.Token{}, false
	}
	var tok forge.Token
	if json.Unmarshal(plain, &tok) != nil || tok.Access == "" {
		return forge.Token{}, false
	}
	return tok, true
}

// Delete forgets a user's token for one host.
func (f *ForgeTokens) Delete(owner, host string) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = os.Remove(f.path(owner, host))
}

// Hosts lists the hosts a user has connected.
func (f *ForgeTokens) Hosts(owner string) []string {
	var out []string
	for _, name := range forge.Names() {
		if _, ok := f.Load(owner, name); ok {
			out = append(out, name)
		}
	}
	return out
}

// path is one user's credential for one host.
//
// The identity is hashed rather than used as a filename. It is an email
// address, which brings characters a path does not want and a length no
// filesystem promises to keep — and a directory listing that is a list of every
// user's address is worth avoiding on its own.
func (f *ForgeTokens) path(owner, host string) string {
	sum := sha256.Sum256([]byte(owner))
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			return r
		}
		return -1
	}, strings.ToLower(host))
	return filepath.Join(f.dir, base64.RawURLEncoding.EncodeToString(sum[:16])+"."+safe+".enc")
}

func (f *ForgeTokens) seal(plain []byte) ([]byte, error) {
	gcm, err := f.gcm()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	// Nonce prepended to the ciphertext: it is not a secret, and keeping the
	// two together means one file rather than a pair that can be separated.
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func (f *ForgeTokens) open(sealed []byte) ([]byte, error) {
	gcm, err := f.gcm()
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, fmt.Errorf("short ciphertext")
	}
	nonce, body := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	return gcm.Open(nil, nonce, body, nil)
}

func (f *ForgeTokens) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(f.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
