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
