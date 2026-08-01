package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// User registry. Access to dross is decided by Cloudflare Access — only accounts
// its policy admits ever complete the OIDC flow — so this is a record of who has
// signed in, not a gate. The first successful sign-in registers the account;
// later ones just update the timestamp.
//
// Keeping it means sessions can be attributed to a stable account rather than a
// bare string, and "who has used this server" is answerable without reading logs.

// User is a registered account, keyed by the verified email from the identity
// provider.
type User struct {
	Email     string    `json:"email"`
	Name      string    `json:"name,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Logins    int       `json:"logins"`
}

// Users is a small JSON-backed registry. The population is a household, so a
// single file read into memory is the right size of solution.
type Users struct {
	path string

	mu sync.Mutex
	by map[string]User // lowercased email -> user
}

// NewUsers opens (or creates) the registry at dir/users.json.
func NewUsers(dir string) (*Users, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	u := &Users{path: filepath.Join(dir, "users.json"), by: map[string]User{}}
	b, err := os.ReadFile(u.path)
	if err != nil {
		if os.IsNotExist(err) {
			return u, nil
		}
		return nil, err
	}
	var list []User
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	for _, x := range list {
		u.by[strings.ToLower(x.Email)] = x
	}
	return u, nil
}

// SignIn registers the account on first sight and records the visit. Returns the
// user and whether this was a sign-up.
func (u *Users) SignIn(email, name string) (User, bool) {
	email = strings.TrimSpace(email)
	if email == "" {
		return User{}, false
	}
	key := strings.ToLower(email)

	u.mu.Lock()
	defer u.mu.Unlock()
	now := time.Now().UTC()
	x, existing := u.by[key]
	if !existing {
		x = User{Email: email, FirstSeen: now}
	}
	if name != "" {
		x.Name = name
	}
	x.LastSeen = now
	x.Logins++
	u.by[key] = x
	u.save()
	return x, !existing
}

// Get returns a registered user.
func (u *Users) Get(email string) (User, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	x, ok := u.by[strings.ToLower(strings.TrimSpace(email))]
	return x, ok
}

// List returns every registered account, oldest first.
func (u *Users) List() []User {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]User, 0, len(u.by))
	for _, x := range u.by {
		out = append(out, x)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].FirstSeen.Before(out[j-1].FirstSeen); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// save writes the registry. Caller holds the lock. A write failure is not fatal:
// losing the record must never block a sign-in that Cloudflare already approved.
func (u *Users) save() {
	list := make([]User, 0, len(u.by))
	for _, x := range u.by {
		list = append(list, x)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	tmp := u.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, u.path) // atomic; never a half-written registry
	}
}
