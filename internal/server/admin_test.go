package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dougkirkley/dross/internal/auth"
	"github.com/dougkirkley/dross/internal/mail"
	"github.com/dougkirkley/dross/internal/storage"
)

// recorder is a Notifier that keeps what it was asked to send.
type recorder struct {
	mu   sync.Mutex
	sent []string // "kind to@example.com"
}

func (r *recorder) Enabled() bool { return true }

func (r *recorder) note(kind string, a mail.Account) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, kind+" "+a.Email)
	return nil
}

func (r *recorder) RequestReceived(_ context.Context, a mail.Account) error {
	return r.note("received", a)
}
func (r *recorder) AdminRequest(_ context.Context, to string, a mail.Account) error {
	return r.note("admin->"+to, a)
}
func (r *recorder) Welcome(_ context.Context, a mail.Account) error { return r.note("welcome", a) }

func (r *recorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sent...)
}

// adminServer is a server with accounts wired and no SSO, so a test can put an
// identity on the request itself. The routes and the admin check are the real
// ones.
func adminServer(t *testing.T) (*Server, *Users, *recorder) {
	t.Helper()
	users, err := NewUsers(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	users.Ensure("doug@example.com", StatusApproved, RoleAdmin)
	mails := &recorder{}
	srv := New(storage.NewSessionStore(t.TempDir()),
		NewManager(&fakeProvider{}, nil, "m", t.TempDir()), nil, testArts(t))
	srv.SetAccounts(users, "doug@example.com", mails)
	return srv, users, mails
}

func as(r *http.Request, email string) *http.Request {
	return r.WithContext(auth.WithIdentity(r.Context(), email))
}

// Being signed in is not what qualifies someone to decide accounts — under the
// approval model every stranger is signed in. And the refusal is a 403: a 404
// would claim the page does not exist.
func TestAdminPageRefusesEveryoneButTheAdmin(t *testing.T) {
	srv, users, _ := adminServer(t)
	users.SignIn("stranger@example.com", "Stranger")

	for _, who := range []string{"stranger@example.com", ""} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, as(httptest.NewRequest("GET", "/admin", nil), who))
		if w.Code != http.StatusForbidden {
			t.Errorf("GET /admin as %q = %d; want 403", who, w.Code)
		}
	}

	// The admin sees the page, and the pending account is on it.
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, as(httptest.NewRequest("GET", "/admin", nil), "DOUG@example.com"))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /admin as the admin = %d; want 200", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "stranger@example.com") ||
		!strings.Contains(body, "Approve") {
		t.Errorf("admin page is missing the pending account or its buttons: %.300s", body)
	}
}

// Approving is the single act that opens the door and says so.
func TestApprovingLetsThemInAndWelcomesThem(t *testing.T) {
	srv, users, mails := adminServer(t)
	users.SignIn("stranger@example.com", "Stranger")

	post := func(email, action, who string) *httptest.ResponseRecorder {
		form := url.Values{"email": {email}, "action": {action}}
		r := httptest.NewRequest("POST", "/admin/decide", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, as(r, who))
		return w
	}

	// A non-admin cannot decide, even for themselves.
	if w := post("stranger@example.com", "approve", "stranger@example.com"); w.Code != http.StatusForbidden {
		t.Fatalf("self-approval = %d; want 403", w.Code)
	}
	if users.IsApproved("stranger@example.com") {
		t.Fatal("a stranger approved themselves")
	}

	w := post("stranger@example.com", "approve", "doug@example.com")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("approve = %d; want 303 back to the page", w.Code)
	}
	if !users.IsApproved("stranger@example.com") {
		t.Fatal("approving did not let them in")
	}
	if got := mails.all(); len(got) != 1 || got[0] != "welcome stranger@example.com" {
		t.Fatalf("mails sent = %v; want one welcome", got)
	}

	// Approving again is a no-op, and must not send a second welcome.
	post("stranger@example.com", "approve", "doug@example.com")
	if got := mails.all(); len(got) != 1 {
		t.Errorf("mails after a repeat approval = %v; want still one", got)
	}

	// Revoking takes access away without deleting the record.
	post("stranger@example.com", "deny", "doug@example.com")
	if users.IsApproved("stranger@example.com") {
		t.Error("denied account still approved")
	}
	if _, ok := users.Get("stranger@example.com"); !ok {
		t.Error("denying deleted the record")
	}
}

// The decision form is a state change on a GET-able page, so a cross-origin POST
// has to be refused even though the session cookie is SameSite=Lax already.
func TestDecideRefusesCrossOriginPosts(t *testing.T) {
	srv, users, _ := adminServer(t)
	users.SignIn("stranger@example.com", "Stranger")

	form := url.Values{"email": {"stranger@example.com"}, "action": {"approve"}}
	r := httptest.NewRequest("POST", "/admin/decide", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, as(r, "doug@example.com"))

	if w.Code != http.StatusForbidden {
		t.Errorf("cross-origin POST = %d; want 403", w.Code)
	}
	if users.IsApproved("stranger@example.com") {
		t.Error("a cross-origin POST approved an account")
	}
}

// A sign-up tells the person it landed and tells the admin someone is waiting.
func TestSignUpAnnouncesToBothSides(t *testing.T) {
	srv, users, mails := adminServer(t)
	u, isNew := users.SignIn("stranger@example.com", "Stranger")
	if !isNew {
		t.Fatal("first sign-in should be a sign-up")
	}
	srv.AnnounceSignUp(u)

	// Sent in the background so a slow mail API cannot delay a login.
	waitFor(t, func() bool { return len(mails.all()) == 2 })
	got := strings.Join(mails.all(), ",")
	if !strings.Contains(got, "received stranger@example.com") ||
		// Addressed to whoever holds the role, found in the directory rather
		// than read out of configuration.
		!strings.Contains(got, "admin->doug@example.com stranger@example.com") {
		t.Errorf("mails sent = %q; want the request and the admin notice", got)
	}
}

// The role on the record is the only grant. An address in a flag used to be
// checked alongside it, which meant a demotion could be undone by a restart.
func TestAdminIsTheRoleNotTheFlag(t *testing.T) {
	users, err := NewUsers(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(storage.NewSessionStore(t.TempDir()),
		NewManager(&fakeProvider{}, nil, "m", t.TempDir()), nil, testArts(t))
	// The flag names Doug, and the directory does not agree.
	srv.SetAccounts(users, "doug@example.com", &recorder{})
	users.SignIn("doug@example.com", "Doug")

	get := func(who string) int {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, as(httptest.NewRequest("GET", "/admin", nil), who))
		return w.Code
	}
	if code := get("doug@example.com"); code != http.StatusForbidden {
		t.Errorf("the flag alone let someone in: /admin = %d; want 403", code)
	}

	// Seeded as the flag intends, they are in.
	users.Ensure("doug@example.com", StatusApproved, RoleAdmin)
	if code := get("doug@example.com"); code != http.StatusOK {
		t.Fatalf("the seeded admin was refused: /admin = %d; want 200", code)
	}

	// Demoted, they are out again - and the flag does not bring them back.
	users.SetRole("doug@example.com", RoleUser, "someone@example.com")
	if code := get("doug@example.com"); code != http.StatusForbidden {
		t.Errorf("a demoted admin still got in: /admin = %d; want 403", code)
	}
}

// waitFor polls until cond holds, for work the server deliberately does in the
// background.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for background work")
}

// The role on the record is what makes an admin, not the flag: that is what
// lets one be handed out later without a redeploy.
func TestAdminIsARoleOnTheRecord(t *testing.T) {
	srv, users, _ := adminServer(t)
	users.SignIn("sarah@example.com", "Sarah")
	users.Decide("sarah@example.com", StatusApproved, "doug@example.com")

	if !users.IsAdmin("doug@example.com") {
		t.Fatal("the seeded admin should hold the admin role")
	}
	if users.IsAdmin("sarah@example.com") {
		t.Fatal("an approved user is not an admin")
	}

	// Approved but not an admin: the API refuses her.
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, as(httptest.NewRequest("GET", "/api/admin/users", nil), "sarah@example.com"))
	if w.Code != http.StatusForbidden {
		t.Errorf("approved non-admin on the admin API = %d; want 403", w.Code)
	}

	// Promoted, she is one — and an admin who is not approved is not an admin.
	users.SetRole("sarah@example.com", RoleAdmin, "doug@example.com")
	if !users.IsAdmin("sarah@example.com") {
		t.Error("promoting did not take effect")
	}
	users.Decide("sarah@example.com", StatusDenied, "doug@example.com")
	if users.IsAdmin("sarah@example.com") {
		t.Error("a denied account still counts as an admin")
	}
}

// The app's surface over the same decisions the page makes.
func TestAdminJSONAPI(t *testing.T) {
	srv, users, mails := adminServer(t)
	users.SignIn("sarah@example.com", "Sarah")

	// whoami is what the client uses to decide whether to offer the screen.
	for who, want := range map[string]bool{"doug@example.com": true, "sarah@example.com": false} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, as(httptest.NewRequest("GET", "/api/whoami", nil), who))
		var body struct {
			Admin bool `json:"admin"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Admin != want {
			t.Errorf("whoami admin for %s = %v; want %v", who, body.Admin, want)
		}
	}

	// The list puts whoever is waiting first, so the app does not have to sort
	// to show the thing the admin came for.
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, as(httptest.NewRequest("GET", "/api/admin/users", nil), "doug@example.com"))
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d; want 200", w.Code)
	}
	var list struct {
		Users []struct {
			Email, Status, Role string
		} `json:"users"`
		Mail bool `json:"mail"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Users) != 2 || list.Users[0].Email != "sarah@example.com" ||
		list.Users[0].Status != "pending" {
		t.Fatalf("list = %+v; want the pending account first", list.Users)
	}
	if list.Users[1].Role != "admin" {
		t.Errorf("the admin's role is not reported: %+v", list.Users[1])
	}

	decide := func(email, action, who string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/admin/users/"+email+"/decide",
			strings.NewReader(`{"action":"`+action+`"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, as(r, who))
		return w
	}

	if w := decide("sarah@example.com", "approve", "sarah@example.com"); w.Code != http.StatusForbidden {
		t.Fatalf("self-approval over the API = %d; want 403", w.Code)
	}
	if w := decide("nobody@example.com", "approve", "doug@example.com"); w.Code != http.StatusNotFound {
		t.Errorf("deciding an unknown account = %d; want 404", w.Code)
	}
	if w := decide("sarah@example.com", "shrug", "doug@example.com"); w.Code != http.StatusBadRequest {
		t.Errorf("unknown action = %d; want 400", w.Code)
	}

	if w := decide("sarah@example.com", "approve", "doug@example.com"); w.Code != http.StatusOK {
		t.Fatalf("approve = %d; want 200", w.Code)
	}
	if !users.IsApproved("sarah@example.com") {
		t.Fatal("the API approval did not take")
	}
	if got := mails.all(); len(got) != 1 || got[0] != "welcome sarah@example.com" {
		t.Fatalf("mails = %v; want one welcome", got)
	}
	// Same decision twice is not two welcomes, over either surface.
	decide("sarah@example.com", "approve", "doug@example.com")
	if got := mails.all(); len(got) != 1 {
		t.Errorf("mails after a repeat = %v; want still one", got)
	}
}

// A server nobody administers cannot approve anyone, so the first authenticated
// person is offered the job — and the offer withdraws itself once taken.
func TestFirstRunClaimsTheAdminRole(t *testing.T) {
	users, err := NewUsers(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(storage.NewSessionStore(t.TempDir()),
		NewManager(&fakeProvider{}, nil, "m", t.TempDir()), nil, testArts(t))
	// No --admin-email: this is the fresh install the page exists for.
	srv.SetAccounts(users, "", &recorder{})

	if users.HasAdmin() {
		t.Fatal("a new registry should have no admin")
	}

	// The page is offered to whoever is signed in.
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, as(httptest.NewRequest("GET", "/setup", nil), "first@example.com"))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "No administrator yet") {
		t.Fatalf("GET /setup = %d, body %.120s", w.Code, w.Body.String())
	}

	claim := func(who string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, as(httptest.NewRequest("POST", "/setup/claim", nil), who))
		return w
	}
	if w := claim("first@example.com"); w.Code != http.StatusSeeOther {
		t.Fatalf("claim = %d; want 303", w.Code)
	}
	if !users.IsAdmin("first@example.com") {
		t.Fatal("the claim did not make them an admin")
	}
	if u, _ := users.Get("first@example.com"); !u.Approved() {
		t.Error("an admin who is not approved is not an admin; the claim must do both")
	}

	// Second person: the role is gone, and they are a normal pending account.
	if w := claim("second@example.com"); w.Code != http.StatusSeeOther {
		t.Errorf("second claim = %d; want a redirect away", w.Code)
	}
	if users.IsAdmin("second@example.com") {
		t.Fatal("two administrators were minted from one first-run page")
	}

	// And the page is no longer on offer.
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, as(httptest.NewRequest("GET", "/setup", nil), "second@example.com"))
	if w.Code != http.StatusFound {
		t.Errorf("GET /setup after the claim = %d; want a redirect away", w.Code)
	}
}

// The claim is decided under one lock, so a race cannot mint two admins.
func TestClaimAdminIsAtomic(t *testing.T) {
	users, err := NewUsers(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const racers = 24
	var wg sync.WaitGroup
	won := make([]bool, racers)
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func(i int) {
			defer wg.Done()
			_, ok := users.ClaimAdmin(fmt.Sprintf("racer%d@example.com", i), "")
			won[i] = ok
		}(i)
	}
	wg.Wait()

	n := 0
	for _, w := range won {
		if w {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d racers claimed the role; want exactly 1", n)
	}
}

// A server with no administrator has to explain itself inside the application,
// not at a URL somebody has to be told about. These are the two calls the app
// makes to do that, and they sit outside the approval gate because the whole
// point is that approval is impossible until one of them is answered.
func TestSetupAPIOffersAndTakesTheRole(t *testing.T) {
	users, err := NewUsers(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(storage.NewSessionStore(t.TempDir()),
		NewManager(&fakeProvider{}, nil, "m", t.TempDir()), nil, testArts(t))
	srv.SetAccounts(users, "", &recorder{})
	users.SignIn("first@example.com", "First")

	call := func(method, path, who string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, as(httptest.NewRequest(method, path, nil), who))
		return w
	}

	w := call("GET", "/api/setup/status", "first@example.com")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"needs_setup":true`) {
		t.Fatalf("status = %d %s", w.Code, w.Body.String())
	}

	if w := call("POST", "/api/setup/claim", "first@example.com"); w.Code != http.StatusOK {
		t.Fatalf("claim = %d %s", w.Code, w.Body.String())
	}
	if !users.IsAdmin("first@example.com") {
		t.Fatal("the claim did not take")
	}

	// Setup is over, and says so.
	if w := call("GET", "/api/setup/status", "second@example.com"); !strings.Contains(w.Body.String(), `"needs_setup":false`) {
		t.Errorf("still asking for setup after a claim: %s", w.Body.String())
	}
	// And a second claim is refused rather than quietly ignored.
	if w := call("POST", "/api/setup/claim", "second@example.com"); w.Code != http.StatusConflict {
		t.Errorf("second claim = %d; want 409", w.Code)
	}
	if users.IsAdmin("second@example.com") {
		t.Fatal("two administrators from one first run")
	}
}

// The predicate that opens the first-run door was once HasAdmin, which is its
// inverse - so with no administrator the one person who could appoint one got
// the holding page, and with an administrator every unapproved account got the
// application shell. Both halves are silent: nothing errors, the wrong page just
// renders. This asserts both directions through the real middleware.
func TestFirstRunOpensOnlyWhenThereIsNoAdmin(t *testing.T) {
	users, err := NewUsers(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(storage.NewSessionStore(t.TempDir()),
		NewManager(&fakeProvider{}, nil, "m", t.TempDir()), nil, testArts(t))
	srv.SetAccounts(users, "", &recorder{})
	users.SignIn("waiting@example.com", "Waiting")

	// The server's own view of it, which is what the app asks.
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, as(httptest.NewRequest("GET", "/api/setup/status", nil), "waiting@example.com"))
	if !strings.Contains(w.Body.String(), `"needs_setup":true`) {
		t.Fatalf("with no admin, setup should be needed: %s", w.Body.String())
	}

	// Appoint one, and the answer has to flip.
	users.Ensure("boss@example.com", StatusApproved, RoleAdmin)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, as(httptest.NewRequest("GET", "/api/setup/status", nil), "waiting@example.com"))
	if !strings.Contains(w.Body.String(), `"needs_setup":false`) {
		t.Fatalf("with an admin, setup is over: %s", w.Body.String())
	}

	// And an account still waiting must not be able to take the role now.
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, as(httptest.NewRequest("POST", "/api/setup/claim", nil), "waiting@example.com"))
	if w.Code != http.StatusConflict {
		t.Errorf("claim with an admin present = %d; want 409", w.Code)
	}
	if users.IsAdmin("waiting@example.com") {
		t.Fatal("an unapproved account promoted itself after setup was over")
	}
}

// Removing accounts, and the two things that must never be removable: yourself,
// and the last person who can approve anyone. Both leave a server that only the
// first-run claim can rescue - and that is offered to whoever turns up next,
// not to the person who meant to do it.
func TestRemoveAndPromote(t *testing.T) {
	srv, users, _ := adminServer(t)
	users.SignIn("sarah@example.com", "Sarah")
	users.Decide("sarah@example.com", StatusApproved, "doug@example.com")

	do := func(method, path, body, who string) *httptest.ResponseRecorder {
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, as(r, who))
		return w
	}

	// A non-admin cannot remove anyone.
	if w := do("DELETE", "/api/admin/users/doug@example.com", "", "sarah@example.com"); w.Code != http.StatusForbidden {
		t.Fatalf("non-admin removal = %d; want 403", w.Code)
	}

	// The admin cannot remove themselves: they hold the session doing it.
	w := do("DELETE", "/api/admin/users/doug@example.com", "", "doug@example.com")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "your own account") {
		t.Fatalf("self-removal = %d %s; want a refusal", w.Code, w.Body.String())
	}
	if _, ok := users.Get("doug@example.com"); !ok {
		t.Fatal("the admin removed themselves")
	}

	// Promotion: Sarah becomes an admin.
	if w := do("POST", "/api/admin/users/sarah@example.com/role", `{"role":"admin"}`, "doug@example.com"); w.Code != http.StatusOK {
		t.Fatalf("promote = %d %s", w.Code, w.Body.String())
	}
	if !users.IsAdmin("sarah@example.com") {
		t.Fatal("promotion did not take")
	}

	// Now there are two, so removing one is allowed.
	if w := do("DELETE", "/api/admin/users/doug@example.com", "", "sarah@example.com"); w.Code != http.StatusOK {
		t.Fatalf("removing one of two admins = %d %s", w.Code, w.Body.String())
	}
	if _, ok := users.Get("doug@example.com"); ok {
		t.Error("the account is still there after a successful removal")
	}

	// And the last one standing can be neither demoted nor removed.
	if w := do("POST", "/api/admin/users/sarah@example.com/role", `{"role":"user"}`, "sarah@example.com"); w.Code != http.StatusConflict {
		t.Errorf("demoting the last admin = %d; want 409", w.Code)
	}
	if !users.IsAdmin("sarah@example.com") {
		t.Fatal("the last administrator demoted themselves")
	}
	users.SignIn("bystander@example.com", "Bystander")
	if w := do("DELETE", "/api/admin/users/sarah@example.com", "", "bystander@example.com"); w.Code != http.StatusForbidden {
		t.Errorf("a bystander removing the admin = %d; want 403", w.Code)
	}
}

// Promoting somebody who is still waiting has to let them in as well. An
// administrator who cannot reach the application is not an administrator, and
// making that two separate clicks means one of them gets forgotten.
func TestPromotingAlsoApproves(t *testing.T) {
	srv, users, _ := adminServer(t)
	users.SignIn("waiting@example.com", "Waiting")
	if users.IsApproved("waiting@example.com") {
		t.Fatal("setup: should still be pending")
	}

	r := httptest.NewRequest("POST", "/api/admin/users/waiting@example.com/role",
		strings.NewReader(`{"role":"admin"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, as(r, "doug@example.com"))

	if w.Code != http.StatusOK {
		t.Fatalf("promote = %d %s", w.Code, w.Body.String())
	}
	if !users.IsApproved("waiting@example.com") {
		t.Error("promoted to admin but still not approved, so they cannot get in")
	}
	if !users.IsAdmin("waiting@example.com") {
		t.Error("promotion did not take")
	}
}

// Kratos registers people without telling dross, so sign-ups are noticed rather
// than witnessed. Each one is announced once, and a restart must not announce
// everybody again - which is why the claim is on the record, not in memory.
func TestSignUpsAreAnnouncedOnce(t *testing.T) {
	srv, users, mails := adminServer(t)
	users.SignIn("stranger@example.com", "Stranger")

	srv.announceNewSignUps()
	waitFor(t, func() bool { return len(mails.all()) == 2 }) // theirs, and the admin's

	// Running again changes nothing: the announcement was claimed.
	srv.announceNewSignUps()
	srv.announceNewSignUps()
	if got := mails.all(); len(got) != 2 {
		t.Fatalf("mails after repeated sweeps = %v; want the original two", got)
	}

	// A restart re-reads the same records, so the claim has to have persisted.
	reopened, err := NewUsers(users.path[:len(users.path)-len("/users.json")])
	if err != nil {
		t.Fatal(err)
	}
	u, ok := reopened.Get("stranger@example.com")
	if !ok || u.AnnouncedAt.IsZero() {
		t.Fatal("the announcement was not recorded, so a restart would mail them again")
	}

	// Somebody already decided is not announced, whatever their record says.
	users.SignIn("decided@example.com", "Decided")
	users.Decide("decided@example.com", StatusApproved, "doug@example.com")
	before := len(mails.all())
	srv.announceNewSignUps()
	if got := mails.all(); len(got) != before {
		t.Errorf("an already-approved account was announced: %v", got)
	}
}

// The limiter exists because the edge allowlist that made this moot was
// removed: the sign-in flow is now reachable by anyone, and each completed
// registration is a database row and two emails.
func TestLimiterStopsALoopAndNotAPerson(t *testing.T) {
	l := newLimiter(60, 20) // a burst of 20, then one a second

	for i := 0; i < 20; i++ {
		if !l.allow("1.2.3.4") {
			t.Fatalf("a person doing a handful of requests was refused at %d", i)
		}
	}
	if l.allow("1.2.3.4") {
		t.Error("a loop past the burst was allowed to continue")
	}
	// One caller's burst must not refuse everybody else.
	if !l.allow("5.6.7.8") {
		t.Error("a second address was refused because the first was busy")
	}
}

// The same-origin check is the second lock on the admin forms. It used to prefer
// X-Forwarded-Host, which is a header the caller sets - so an attacker could
// send an Origin of their choosing and a matching X-Forwarded-Host, and the
// check would agree with itself.
func TestSameOriginIgnoresHeadersTheCallerControls(t *testing.T) {
	srv, users, _ := adminServer(t)
	users.SignIn("stranger@example.com", "Stranger")
	srv.SetPublicURL("https://dross.sh")

	forge := func() *http.Request {
		form := "email=stranger%40example.com&action=approve"
		r := httptest.NewRequest("POST", "/admin/decide", strings.NewReader(form))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "https://evil.example")
		r.Header.Set("X-Forwarded-Host", "evil.example")
		r.Host = "evil.example"
		return r
	}

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, as(forge(), "doug@example.com"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("a forged origin was accepted: %d", w.Code)
	}
	if users.IsApproved("stranger@example.com") {
		t.Fatal("a cross-origin post approved an account by agreeing with itself")
	}

	// And the real origin still works, or the lock is on the wrong door.
	good := httptest.NewRequest("POST", "/admin/decide",
		strings.NewReader("email=stranger%40example.com&action=approve"))
	good.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	good.Header.Set("Origin", "https://dross.sh")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, as(good, "doug@example.com"))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("a legitimate submission was refused: %d %s", w.Code, w.Body.String())
	}
	if !users.IsApproved("stranger@example.com") {
		t.Error("the legitimate approval did not land")
	}
}
