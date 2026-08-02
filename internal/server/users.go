package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// User registry, and the authorization half of the standard SaaS split: the
// identity provider says who someone is, dross says what they may do. The first
// successful sign-in registers the account as pending; an admin approves it.
//
// Keeping it means sessions can be attributed to a stable account rather than a
// bare string, and "who has used this server" is answerable without reading logs.

// Status is where an account stands with the application. The zero value is
// deliberately not a valid status: a record loaded without one predates the
// approval gate and is migrated to StatusApproved, so an upgrade cannot lock
// out the people already using the server.
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusDenied   Status = "denied"
)

// User is a registered account, keyed by the verified email from the identity
// provider.
type User struct {
	Email      string    `json:"email"`
	Name       string    `json:"name,omitempty"`
	Status     Status    `json:"status"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	Logins     int       `json:"logins"`
	DecidedAt  time.Time `json:"decided_at,omitempty"`
	DecidedBy  string    `json:"decided_by,omitempty"`
	WelcomedAt time.Time `json:"welcomed_at,omitempty"` // welcome mail sent; never sent twice
}

// Approved reports whether the account may use the application.
func (u User) Approved() bool { return u.Status == StatusApproved }

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
	migrated := false
	for _, x := range list {
		// Written before the approval gate existed: everyone on disk was, by
		// definition, allowed in. Anything else logs out the whole household on
		// the deploy that introduces this.
		if x.Status == "" {
			x.Status = StatusApproved
			migrated = true
		}
		u.by[strings.ToLower(x.Email)] = x
	}
	if migrated {
		u.mu.Lock()
		u.save()
		u.mu.Unlock()
	}
	return u, nil
}

// SignIn registers the account on first sight and records the visit. Returns the
// user and whether this was a sign-up. A new account is pending: signing in is
// how you ask for access, not how you get it.
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
		x = User{Email: email, Status: StatusPending, FirstSeen: now}
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

// IsApproved reports whether email belongs to an approved account. An unknown
// email is not approved: the gate is a positive check, so a registry that failed
// to record someone refuses them rather than waving them through.
func (u *Users) IsApproved(email string) bool {
	x, ok := u.Get(email)
	return ok && x.Approved()
}

// Decide sets an account's status. by is the admin who decided, recorded so the
// registry says who let someone in. Returns the updated user, and whether this
// actually changed anything — approving an already-approved account is a no-op
// and must not send a second welcome.
func (u *Users) Decide(email string, status Status, by string) (User, bool) {
	key := strings.ToLower(strings.TrimSpace(email))
	u.mu.Lock()
	defer u.mu.Unlock()
	x, ok := u.by[key]
	if !ok || x.Status == status {
		return x, false
	}
	x.Status = status
	x.DecidedAt = time.Now().UTC()
	x.DecidedBy = by
	u.by[key] = x
	u.save()
	return x, true
}

// Ensure registers email at the given status if it is not known yet, and lifts
// an existing account up to it. Used to seed the admin, who must be approved
// before anyone is there to approve them.
func (u *Users) Ensure(email string, status Status) {
	email = strings.TrimSpace(email)
	if email == "" {
		return
	}
	key := strings.ToLower(email)
	u.mu.Lock()
	defer u.mu.Unlock()
	x, ok := u.by[key]
	if ok && x.Status == status {
		return
	}
	if !ok {
		x = User{Email: email, FirstSeen: time.Now().UTC()}
	}
	x.Status = status
	u.by[key] = x
	u.save()
}

// MarkWelcomed records that the welcome mail went out, so a second approval (or
// a restart mid-send) does not mail the user twice. Reports whether this call
// was the one that claimed the send.
func (u *Users) MarkWelcomed(email string) bool {
	key := strings.ToLower(strings.TrimSpace(email))
	u.mu.Lock()
	defer u.mu.Unlock()
	x, ok := u.by[key]
	if !ok || !x.WelcomedAt.IsZero() {
		return false
	}
	x.WelcomedAt = time.Now().UTC()
	u.by[key] = x
	u.save()
	return true
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
