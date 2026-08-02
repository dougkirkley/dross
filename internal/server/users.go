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

// Role is what an account may do once it is in. Kept separate from Status
// because they answer different questions — may you be here, and may you decide
// who else can. A record loaded without one is a plain user.
//
// Two roles is not a permission system, and is not pretending to be one. It is
// the smallest thing that stops "admin" meaning "whichever address happens to be
// in a flag", so an admin can be promoted from the UI instead of a redeploy.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// User is a registered account, keyed by the verified email from the identity
// provider.
type User struct {
	Email      string    `json:"email"`
	Name       string    `json:"name,omitempty"`
	Status     Status    `json:"status"`
	Role       Role      `json:"role"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	Logins     int       `json:"logins"`
	DecidedAt  time.Time `json:"decided_at,omitempty"`
	DecidedBy  string    `json:"decided_by,omitempty"`
	WelcomedAt time.Time `json:"welcomed_at,omitempty"` // welcome mail sent; never sent twice
	// AnnouncedAt is when the sign-up was reported to the administrators. On the
	// record rather than in memory, so a restart does not report everyone again.
	AnnouncedAt time.Time `json:"announced_at,omitempty"`
}

// Approved reports whether the account may use the application.
func (u User) Approved() bool { return u.Status == StatusApproved }

// IsAdmin reports whether the account may decide other accounts. An admin who
// is not approved is not an admin: the gate comes first.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin && u.Approved() }

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
		if x.Role == "" {
			x.Role = RoleUser
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
		x = User{Email: email, Status: StatusPending, Role: RoleUser, FirstSeen: now}
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

// Ensure registers email at the given status and role if it is not known yet,
// and lifts an existing account up to them. Used to seed the admin, who has to
// be approved before anyone exists to approve them.
func (u *Users) Ensure(email string, status Status, role Role) {
	email = strings.TrimSpace(email)
	if email == "" {
		return
	}
	key := strings.ToLower(email)
	u.mu.Lock()
	defer u.mu.Unlock()
	x, ok := u.by[key]
	if ok && x.Status == status && x.Role == role {
		return
	}
	if !ok {
		x = User{Email: email, FirstSeen: time.Now().UTC()}
	}
	x.Status, x.Role = status, role
	u.by[key] = x
	u.save()
}

// IsAdmin reports whether email belongs to an approved admin.
func (u *Users) IsAdmin(email string) bool {
	x, ok := u.Get(email)
	return ok && x.IsAdmin()
}

// HasAdmin reports whether anyone can approve anyone. False is the first-run
// state: an empty registry, or one whose admin was never seeded because the
// flag was left unset.
func (u *Users) HasAdmin() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, x := range u.by {
		if x.IsAdmin() {
			return true
		}
	}
	return false
}

// ClaimAdmin makes email the first administrator, and reports whether it got
// it. Refuses once anyone holds the role, under the same lock that reads it, so
// two people racing the first-run page cannot both come away as admin.
func (u *Users) ClaimAdmin(email, name string) (User, bool) {
	email = strings.TrimSpace(email)
	if email == "" {
		return User{}, false
	}
	key := strings.ToLower(email)

	u.mu.Lock()
	defer u.mu.Unlock()
	for _, x := range u.by {
		if x.IsAdmin() {
			return User{}, false
		}
	}
	now := time.Now().UTC()
	x, ok := u.by[key]
	if !ok {
		x = User{Email: email, FirstSeen: now, LastSeen: now}
	}
	if name != "" && x.Name == "" {
		x.Name = name
	}
	x.Status, x.Role = StatusApproved, RoleAdmin
	x.DecidedAt, x.DecidedBy = now, x.Email // claimed it themselves; say so
	u.by[key] = x
	u.save()
	return x, true
}

// SetRole promotes or demotes an account. Reports whether anything changed, so
// a no-op does not read as an action in the log.
func (u *Users) SetRole(email string, role Role, by string) (User, bool) {
	key := strings.ToLower(strings.TrimSpace(email))
	u.mu.Lock()
	defer u.mu.Unlock()
	x, ok := u.by[key]
	if !ok || x.Role == role {
		return x, false
	}
	x.Role = role
	x.DecidedAt = time.Now().UTC()
	x.DecidedBy = by
	u.by[key] = x
	u.save()
	return x, true
}

// MarkAnnounced records that a sign-up has been reported, and reports whether
// this call claimed it.
func (u *Users) MarkAnnounced(email string) bool {
	key := strings.ToLower(strings.TrimSpace(email))
	u.mu.Lock()
	defer u.mu.Unlock()
	x, ok := u.by[key]
	if !ok || !x.AnnouncedAt.IsZero() {
		return false
	}
	x.AnnouncedAt = time.Now().UTC()
	u.by[key] = x
	u.save()
	return true
}

// Remove deletes an account. Reports whether there was one to delete, so a
// double-click does not read as two removals.
func (u *Users) Remove(email string) bool {
	key := strings.ToLower(strings.TrimSpace(email))
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.by[key]; !ok {
		return false
	}
	delete(u.by, key)
	u.save()
	return true
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
