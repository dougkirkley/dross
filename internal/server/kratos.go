package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The account directory backed by Ory Kratos, as an alternative to the JSON
// registry in users.go. Same interface, so nothing above it knows which one it
// is talking to.
//
// Two things shape this file.
//
// The first is that the approval gate runs on every single request. Against a
// map that is a lock and a lookup; against an HTTP API on top of Postgres it is
// a network round trip, and a database restart would otherwise mean the whole
// application stops answering. So reads come from a cache of the whole
// directory — a household fits in memory many times over — refreshed on a timer
// and, when Kratos cannot be reached, deliberately served stale rather than
// failed. An unknown account is still refused: staleness extends what was true,
// it does not invent permission.
//
// The second is that Kratos's admin API has no authentication of its own.
// Anything that can reach it can mint an administrator, which is why it is a
// ClusterIP with a NetworkPolicy in front and why this client refuses to talk
// to anything but plain HTTP inside the cluster.

// kratosTTL is how long the cached directory is trusted before a refresh is
// attempted. Short, because it bounds how long a revoked account keeps working.
const kratosTTL = 30 * time.Second

// KratosDirectory implements UserDirectory (and auth.Approver) against Kratos.
type KratosDirectory struct {
	admin  string // e.g. http://kratos-admin.dross-system.svc
	client *http.Client

	// AllowCreate lets dross create identities through the admin API. True only
	// while dross owns the login: an identity it creates has no credentials
	// attached, because credentials come from an actual sign-in and there has
	// not been one.
	//
	// Once Kratos owns the login, such a record is worse than useless. Nothing
	// can ever authenticate to it, and because the email is a credential
	// identifier it collides with the identity Kratos tries to create from a
	// real Google sign-in - which fails with "that email is already used by
	// another account", offering to link it to the credentials it does not
	// have. Creating accounts is Kratos's job from then on; dross only updates
	// what it finds.
	AllowCreate bool

	// claim serialises ClaimAdmin. dross runs one replica by construction — the
	// Deployment is Recreate and its volumes are ReadWriteOnce — so a mutex is
	// the honest scope of this lock. It is not a distributed one and must not be
	// mistaken for one if that ever changes.
	claim sync.Mutex

	mu      sync.RWMutex
	byEmail map[string]kratosIdentity
	fetched time.Time
	stale   bool // last refresh failed; serving what we had
}

// NewKratosDirectory returns a directory backed by the admin API at adminURL.
func NewKratosDirectory(adminURL string) (*KratosDirectory, error) {
	adminURL = strings.TrimRight(strings.TrimSpace(adminURL), "/")
	if adminURL == "" {
		return nil, fmt.Errorf("kratos admin URL is empty")
	}
	u, err := url.Parse(adminURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("kratos admin URL %q is not a URL", adminURL)
	}
	return &KratosDirectory{
		admin:   adminURL,
		client:  &http.Client{Timeout: 10 * time.Second},
		byEmail: map[string]kratosIdentity{},
	}, nil
}

// --- wire types --------------------------------------------------------------

// kratosIdentity is the part of an identity dross cares about. Kratos owns
// traits and timestamps; everything dross decides lives under metadata_admin,
// which is readable and writable only through the admin API — so an account
// cannot promote itself by editing its own profile.
type kratosIdentity struct {
	ID     string `json:"id,omitempty"`
	State  string `json:"state,omitempty"`
	Traits struct {
		Email string `json:"email"`
		Name  string `json:"name,omitempty"`
	} `json:"traits"`
	MetadataAdmin kratosMeta `json:"metadata_admin"`
	CreatedAt     time.Time  `json:"created_at,omitempty"`
}

type kratosMeta struct {
	Status     Status    `json:"status,omitempty"`
	Role       Role      `json:"role,omitempty"`
	LastSeen   time.Time `json:"last_seen,omitempty"`
	Logins     int       `json:"logins,omitempty"`
	DecidedAt  time.Time `json:"decided_at,omitempty"`
	DecidedBy  string    `json:"decided_by,omitempty"`
	WelcomedAt time.Time `json:"welcomed_at,omitempty"`
}

// user projects an identity into the shape the rest of the server uses.
func (k kratosIdentity) user() User {
	m := k.MetadataAdmin
	// An identity Kratos created that dross has never seen has no metadata at
	// all. It is a sign-up: pending, like any other.
	status, role := m.Status, m.Role
	if status == "" {
		status = StatusPending
	}
	if role == "" {
		role = RoleUser
	}
	return User{
		Email:      k.Traits.Email,
		Name:       k.Traits.Name,
		Status:     status,
		Role:       role,
		FirstSeen:  k.CreatedAt,
		LastSeen:   m.LastSeen,
		Logins:     m.Logins,
		DecidedAt:  m.DecidedAt,
		DecidedBy:  m.DecidedBy,
		WelcomedAt: m.WelcomedAt,
	}
}

// --- transport ---------------------------------------------------------------

func (d *KratosDirectory) do(method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, d.admin+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("kratos %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(buf.String()))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// fetch pulls the whole directory. Paged, because the admin API caps a page and
// silently returning the first 250 accounts would be a gate that forgets people.
func (d *KratosDirectory) fetch() (map[string]kratosIdentity, error) {
	out := map[string]kratosIdentity{}
	page := ""
	for {
		path := "/admin/identities?page_size=250"
		if page != "" {
			path += "&page_token=" + url.QueryEscape(page)
		}
		var batch []kratosIdentity
		if err := d.do(http.MethodGet, path, nil, &batch); err != nil {
			return nil, err
		}
		for _, id := range batch {
			if e := strings.ToLower(strings.TrimSpace(id.Traits.Email)); e != "" {
				out[e] = id
			}
		}
		// Kratos pages with a token in the Link header. Without a full page
		// there is nothing more to ask for, which is the condition that matters
		// here and does not need the header parsed.
		if len(batch) < 250 {
			return out, nil
		}
		page = batch[len(batch)-1].ID
	}
}

// snapshot returns the cached directory, refreshing it when it is older than
// the TTL. A failed refresh keeps the previous copy: an identity service that
// is briefly unreachable must not log out a household.
func (d *KratosDirectory) snapshot() map[string]kratosIdentity {
	d.mu.RLock()
	fresh := time.Since(d.fetched) < kratosTTL && d.fetched != time.Time{}
	cached := d.byEmail
	d.mu.RUnlock()
	if fresh {
		return cached
	}

	got, err := d.fetch()
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil {
		// Hold the old copy and stop hammering a service that is down: the next
		// attempt waits another TTL.
		d.fetched = time.Now()
		if !d.stale {
			d.stale = true
			fmt.Printf("kratos unreachable, serving the last known directory: %v\n", err)
		}
		return d.byEmail
	}
	if d.stale {
		d.stale = false
		fmt.Println("kratos reachable again")
	}
	d.byEmail, d.fetched = got, time.Now()
	return got
}

// put writes an identity back and updates the cache, so the next read reflects
// a decision that was just made rather than waiting out the TTL.
func (d *KratosDirectory) put(id kratosIdentity) (User, error) {
	body := map[string]any{
		"schema_id":      "default",
		"state":          id.State,
		"traits":         id.Traits,
		"metadata_admin": id.MetadataAdmin,
	}
	var updated kratosIdentity
	if err := d.do(http.MethodPut, "/admin/identities/"+url.PathEscape(id.ID), body, &updated); err != nil {
		return User{}, err
	}
	d.cache(updated)
	return updated.user(), nil
}

func (d *KratosDirectory) cache(id kratosIdentity) {
	e := strings.ToLower(strings.TrimSpace(id.Traits.Email))
	if e == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.byEmail == nil {
		d.byEmail = map[string]kratosIdentity{}
	}
	d.byEmail[e] = id
}

// find returns the raw identity for an email.
func (d *KratosDirectory) find(email string) (kratosIdentity, bool) {
	id, ok := d.snapshot()[strings.ToLower(strings.TrimSpace(email))]
	return id, ok
}

// --- UserDirectory -----------------------------------------------------------

// SignIn records the visit, creating the identity if Kratos has never seen it.
// Once Kratos owns the login flow it creates identities itself and this only
// ever updates; until then dross's own OIDC callback is what registers people,
// so both paths have to work.
func (d *KratosDirectory) SignIn(email, name string) (User, bool) {
	email = strings.TrimSpace(email)
	if email == "" {
		return User{}, false
	}
	now := time.Now().UTC()

	if id, ok := d.find(email); ok {
		if name != "" && id.Traits.Name == "" {
			id.Traits.Name = name
		}
		id.MetadataAdmin.LastSeen = now
		id.MetadataAdmin.Logins++
		u, err := d.put(id)
		if err != nil {
			// The visit is not worth failing a login over: report what we knew.
			return id.user(), false
		}
		return u, false
	}

	if !d.AllowCreate {
		// Kratos registers people now. A sign-in for someone it has never heard
		// of should not happen, and inventing a record for them would only
		// block the registration that will.
		return User{Email: email, Name: name, Status: StatusPending, Role: RoleUser}, false
	}

	var id kratosIdentity
	id.Traits.Email = email
	id.Traits.Name = name
	id.State = "active" // may authenticate; whether they may act is the status
	id.MetadataAdmin = kratosMeta{Status: StatusPending, Role: RoleUser, LastSeen: now, Logins: 1}
	body := map[string]any{
		"schema_id":      "default",
		"state":          id.State,
		"traits":         id.Traits,
		"metadata_admin": id.MetadataAdmin,
	}
	var created kratosIdentity
	if err := d.do(http.MethodPost, "/admin/identities", body, &created); err != nil {
		// Losing the record must never block a sign-in the provider allowed.
		return id.user(), false
	}
	d.cache(created)
	return created.user(), true
}

func (d *KratosDirectory) List() []User {
	all := d.snapshot()
	out := make([]User, 0, len(all))
	for _, id := range all {
		out = append(out, id.user())
	}
	// Oldest first, matching the JSON registry so the admin page does not
	// reorder itself when the store changes underneath it.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].FirstSeen.Before(out[j-1].FirstSeen); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (d *KratosDirectory) Get(email string) (User, bool) {
	id, ok := d.find(email)
	if !ok {
		return User{}, false
	}
	return id.user(), true
}

func (d *KratosDirectory) IsApproved(email string) bool {
	u, ok := d.Get(email)
	return ok && u.Approved()
}

func (d *KratosDirectory) IsAdmin(email string) bool {
	u, ok := d.Get(email)
	return ok && u.IsAdmin()
}

func (d *KratosDirectory) HasAdmin() bool {
	for _, id := range d.snapshot() {
		if id.user().IsAdmin() {
			return true
		}
	}
	return false
}

func (d *KratosDirectory) Decide(email string, status Status, by string) (User, bool) {
	id, ok := d.find(email)
	if !ok {
		return User{}, false
	}
	if id.MetadataAdmin.Status == status {
		return id.user(), false
	}
	id.MetadataAdmin.Status = status
	id.MetadataAdmin.DecidedAt = time.Now().UTC()
	id.MetadataAdmin.DecidedBy = by
	u, err := d.put(id)
	if err != nil {
		return id.user(), false
	}
	return u, true
}

// Remove deletes the identity. Kratos invalidates its sessions as part of that,
// so a removed account is signed out rather than merely forgotten - which is
// the whole point of removing somebody rather than denying them.
func (d *KratosDirectory) Remove(email string) bool {
	id, ok := d.find(email)
	if !ok {
		return false
	}
	if err := d.do(http.MethodDelete, "/admin/identities/"+url.PathEscape(id.ID), nil, nil); err != nil {
		return false
	}
	d.mu.Lock()
	delete(d.byEmail, strings.ToLower(strings.TrimSpace(email)))
	d.mu.Unlock()
	return true
}

func (d *KratosDirectory) MarkWelcomed(email string) bool {
	id, ok := d.find(email)
	if !ok || !id.MetadataAdmin.WelcomedAt.IsZero() {
		return false
	}
	id.MetadataAdmin.WelcomedAt = time.Now().UTC()
	if _, err := d.put(id); err != nil {
		return false
	}
	return true
}

// Ensure lifts an account to a status and role, creating it if needed. Seeds the
// configured admin, who otherwise waits on an approval nobody can give.
func (d *KratosDirectory) Ensure(email string, status Status, role Role) {
	email = strings.TrimSpace(email)
	if email == "" {
		return
	}
	if id, ok := d.find(email); ok {
		if id.MetadataAdmin.Status == status && id.MetadataAdmin.Role == role {
			return
		}
		id.MetadataAdmin.Status, id.MetadataAdmin.Role = status, role
		_, _ = d.put(id)
		return
	}
	if !d.AllowCreate {
		// Seeding an admin who has never signed in would create exactly the
		// credential-less record that breaks their first sign-in. They will be
		// offered the role by the first-run page instead.
		return
	}
	var id kratosIdentity
	id.Traits.Email = email
	id.State = "active"
	id.MetadataAdmin = kratosMeta{Status: status, Role: role}
	body := map[string]any{
		"schema_id":      "default",
		"state":          id.State,
		"traits":         id.Traits,
		"metadata_admin": id.MetadataAdmin,
	}
	var created kratosIdentity
	if err := d.do(http.MethodPost, "/admin/identities", body, &created); err == nil {
		d.cache(created)
	}
}

// SetRole promotes or demotes an account.
func (d *KratosDirectory) SetRole(email string, role Role, by string) (User, bool) {
	id, ok := d.find(email)
	if !ok || id.MetadataAdmin.Role == role {
		return User{}, false
	}
	id.MetadataAdmin.Role = role
	id.MetadataAdmin.DecidedAt = time.Now().UTC()
	id.MetadataAdmin.DecidedBy = by
	u, err := d.put(id)
	if err != nil {
		return User{}, false
	}
	return u, true
}

// ClaimAdmin makes the caller the first administrator, if there is not one.
//
// The check and the write are not one operation in Kratos the way they are
// under a mutex over a map, so this re-reads the directory from the API rather
// than the cache before deciding: a stale copy is exactly how two people both
// come away thinking they won.
func (d *KratosDirectory) ClaimAdmin(email, name string) (User, bool) {
	email = strings.TrimSpace(email)
	if email == "" {
		return User{}, false
	}
	d.claim.Lock()
	defer d.claim.Unlock()

	current, err := d.fetch()
	if err != nil {
		// Refusing is the safe direction: nobody becomes an administrator
		// because the store could not be read.
		return User{}, false
	}
	d.mu.Lock()
	d.byEmail, d.fetched, d.stale = current, time.Now(), false
	d.mu.Unlock()
	for _, id := range current {
		if id.user().IsAdmin() {
			return User{}, false
		}
	}

	key := strings.ToLower(email)
	id, ok := current[key]
	if !ok {
		id.Traits.Email = email
		id.Traits.Name = name
		id.State = "active"
	} else if name != "" && id.Traits.Name == "" {
		id.Traits.Name = name
	}
	now := time.Now().UTC()
	id.MetadataAdmin.Status, id.MetadataAdmin.Role = StatusApproved, RoleAdmin
	id.MetadataAdmin.DecidedAt, id.MetadataAdmin.DecidedBy = now, email

	if ok {
		u, err := d.put(id)
		return u, err == nil
	}
	body := map[string]any{
		"schema_id":      "default",
		"state":          id.State,
		"traits":         id.Traits,
		"metadata_admin": id.MetadataAdmin,
	}
	var created kratosIdentity
	if err := d.do(http.MethodPost, "/admin/identities", body, &created); err != nil {
		return User{}, false
	}
	d.cache(created)
	return created.user(), true
}
