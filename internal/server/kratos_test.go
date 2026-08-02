package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeKratos is enough of the admin API to exercise the directory: identities
// keyed by id, created and replaced whole, listed in one page.
type fakeKratos struct {
	mu    sync.Mutex
	ids   map[string]kratosIdentity
	next  int
	down  bool // refuse everything, to test the cache falling back
	calls int  // list calls, to prove the cache is actually caching
}

func newFakeKratos(t *testing.T) (*fakeKratos, *KratosDirectory) {
	t.Helper()
	f := &fakeKratos{ids: map[string]kratosIdentity{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	d, err := NewKratosDirectory(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	// These exercise the mode where dross still owns the login and therefore
	// registers people itself. The other mode has a test of its own below.
	d.AllowCreate = true
	return f, d
}

func (f *fakeKratos) setDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

func (f *fakeKratos) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		http.Error(w, "kratos is having a moment", http.StatusServiceUnavailable)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/admin/identities":
		f.calls++
		out := make([]kratosIdentity, 0, len(f.ids))
		for _, id := range f.ids {
			out = append(out, id)
		}
		_ = json.NewEncoder(w).Encode(out)

	case r.Method == http.MethodPost && r.URL.Path == "/admin/identities":
		var id kratosIdentity
		_ = json.NewDecoder(r.Body).Decode(&id)
		f.next++
		id.ID = "id-" + strings.Repeat("0", 2) + string(rune('0'+f.next))
		id.CreatedAt = time.Now().UTC()
		f.ids[id.ID] = id
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(id)

	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/admin/identities/"):
		want := strings.TrimPrefix(r.URL.Path, "/admin/identities/")
		old, ok := f.ids[want]
		if !ok {
			http.Error(w, "no such identity", http.StatusNotFound)
			return
		}
		var id kratosIdentity
		_ = json.NewDecoder(r.Body).Decode(&id)
		id.ID, id.CreatedAt = old.ID, old.CreatedAt
		f.ids[id.ID] = id
		_ = json.NewEncoder(w).Encode(id)

	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

// The same contract the JSON registry keeps, over HTTP: a first sign-in
// registers a pending account, deciding is idempotent, and the welcome is
// claimed once.
func TestKratosDirectoryKeepsTheRegistryContract(t *testing.T) {
	_, d := newFakeKratos(t)

	got, isNew := d.SignIn("New@Example.com", "New Person")
	if !isNew || got.Status != StatusPending || got.Role != RoleUser {
		t.Fatalf("first sign-in: isNew=%v %+v", isNew, got)
	}
	if d.IsApproved("new@example.com") {
		t.Fatal("a new account must not be approved")
	}
	// Case must not create a second identity.
	if _, isNew := d.SignIn("new@example.com", ""); isNew {
		t.Error("second sign-in registered a new account")
	}
	if n := len(d.List()); n != 1 {
		t.Fatalf("List = %d accounts; want 1", n)
	}

	u, changed := d.Decide("NEW@example.com", StatusApproved, "doug@example.com")
	if !changed || !u.Approved() || u.DecidedBy != "doug@example.com" {
		t.Fatalf("approve: changed=%v %+v", changed, u)
	}
	if !d.IsApproved("new@example.com") {
		t.Fatal("approving did not take")
	}
	if _, changed := d.Decide("new@example.com", StatusApproved, "doug@example.com"); changed {
		t.Error("approving an approved account reported a change")
	}
	if !d.MarkWelcomed("new@example.com") || d.MarkWelcomed("new@example.com") {
		t.Error("the welcome must be claimable exactly once")
	}

	// Unknown accounts are refused rather than assumed.
	if d.IsApproved("ghost@example.com") || d.IsAdmin("ghost@example.com") {
		t.Error("an unknown account came back approved or admin")
	}
}

// Roles: seeded admin, promotion, and the rule that an unapproved admin is not
// an admin.
func TestKratosDirectoryRoles(t *testing.T) {
	_, d := newFakeKratos(t)

	if d.HasAdmin() {
		t.Fatal("an empty directory has no admin")
	}
	d.Ensure("doug@example.com", StatusApproved, RoleAdmin)
	if !d.IsAdmin("doug@example.com") || !d.HasAdmin() {
		t.Fatal("the seeded admin did not take")
	}
	// Ensure is idempotent.
	d.Ensure("doug@example.com", StatusApproved, RoleAdmin)
	if n := len(d.List()); n != 1 {
		t.Errorf("Ensure created %d accounts; want 1", n)
	}

	d.SignIn("sarah@example.com", "Sarah")
	d.Decide("sarah@example.com", StatusApproved, "doug@example.com")
	if d.IsAdmin("sarah@example.com") {
		t.Error("an approved user is not an admin")
	}
	if _, ok := d.SetRole("sarah@example.com", RoleAdmin, "doug@example.com"); !ok {
		t.Fatal("promotion failed")
	}
	if !d.IsAdmin("sarah@example.com") {
		t.Error("promotion did not take")
	}
	d.Decide("sarah@example.com", StatusDenied, "doug@example.com")
	if d.IsAdmin("sarah@example.com") {
		t.Error("a denied account still counts as an admin")
	}
}

// The first-run claim, and the rule that it can only be won once.
func TestKratosClaimAdmin(t *testing.T) {
	_, d := newFakeKratos(t)

	if _, ok := d.ClaimAdmin("first@example.com", "First"); !ok {
		t.Fatal("the first claim should win")
	}
	if !d.IsAdmin("first@example.com") {
		t.Fatal("the claim did not make them an admin")
	}
	if u, _ := d.Get("first@example.com"); !u.Approved() {
		t.Error("claiming must approve as well as promote")
	}
	if _, ok := d.ClaimAdmin("second@example.com", "Second"); ok {
		t.Fatal("a second claim won an already-taken role")
	}
	if d.IsAdmin("second@example.com") {
		t.Fatal("two administrators from one first-run page")
	}
}

// The availability property: reads survive Kratos going away, without inventing
// permission for anyone it never told us about.
func TestKratosDirectoryServesStaleWhenKratosIsDown(t *testing.T) {
	f, d := newFakeKratos(t)
	d.SignIn("doug@example.com", "Doug")
	d.Decide("doug@example.com", StatusApproved, "doug@example.com")
	if !d.IsApproved("doug@example.com") {
		t.Fatal("setup: should be approved")
	}

	f.setDown(true)
	// Force the cache past its TTL so the next read must try, and fail, to
	// refresh.
	d.mu.Lock()
	d.fetched = time.Now().Add(-2 * kratosTTL)
	d.mu.Unlock()

	if !d.IsApproved("doug@example.com") {
		t.Error("an approved account was refused because the store was unreachable")
	}
	if d.IsApproved("stranger@example.com") {
		t.Error("an unknown account was let in while the store was unreachable")
	}
	if n := len(d.List()); n != 1 {
		t.Errorf("List while down = %d; want the cached 1", n)
	}

	// And nobody becomes an administrator on the strength of a store that
	// cannot be read.
	if _, ok := d.ClaimAdmin("opportunist@example.com", ""); ok {
		t.Error("the admin role was claimed while Kratos was unreachable")
	}

	f.setDown(false)
	d.mu.Lock()
	d.fetched = time.Now().Add(-2 * kratosTTL)
	d.mu.Unlock()
	if !d.IsApproved("doug@example.com") {
		t.Error("recovery: still refusing after Kratos came back")
	}
}

// The gate runs on every request, so it must not be one HTTP call per request.
func TestKratosDirectoryCachesReads(t *testing.T) {
	f, d := newFakeKratos(t)
	d.SignIn("doug@example.com", "Doug")

	f.mu.Lock()
	f.calls = 0
	f.mu.Unlock()

	for i := 0; i < 50; i++ {
		d.IsApproved("doug@example.com")
	}
	f.mu.Lock()
	calls := f.calls
	f.mu.Unlock()
	if calls > 1 {
		t.Errorf("50 approval checks made %d list calls; want at most 1", calls)
	}
}

// Once Kratos owns the login, dross must not create identities. One it makes
// has no credentials - they come from a sign-in that has not happened - and
// because the email is a credential identifier, that empty record collides with
// the one Kratos builds from the real Google sign-in. The person is then told
// their email is already in use and offered a link to credentials that do not
// exist, which is a dead end no amount of retrying escapes.
func TestKratosDirectoryDoesNotCreateWhenKratosOwnsLogin(t *testing.T) {
	f, d := newFakeKratos(t)
	d.AllowCreate = false

	u, isNew := d.SignIn("stranger@example.com", "Stranger")
	if isNew {
		t.Error("dross registered an account Kratos has never heard of")
	}
	// It still reports something sensible for the caller mid-request.
	if u.Email != "stranger@example.com" || u.Status != StatusPending {
		t.Errorf("SignIn returned %+v; want a pending stand-in", u)
	}

	// Seeding an admin who has never signed in is the same trap.
	d.Ensure("never-seen@example.com", StatusApproved, RoleAdmin)

	f.mu.Lock()
	n := len(f.ids)
	f.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d identities were created; want none - Kratos registers people now", n)
	}
}
