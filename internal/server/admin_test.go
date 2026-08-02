package server

import (
	"context"
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
func (r *recorder) AdminRequest(_ context.Context, a mail.Account) error { return r.note("admin", a) }
func (r *recorder) Welcome(_ context.Context, a mail.Account) error      { return r.note("welcome", a) }

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
	users.Ensure("doug@example.com", StatusApproved)
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
		!strings.Contains(got, "admin stranger@example.com") {
		t.Errorf("mails sent = %q; want the request and the admin notice", got)
	}
}

// With no admin configured there is no admin surface at all.
func TestNoAdminEmailMeansNoAdminRoutes(t *testing.T) {
	users, err := NewUsers(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(storage.NewSessionStore(t.TempDir()),
		NewManager(&fakeProvider{}, nil, "m", t.TempDir()), nil, testArts(t))
	srv.SetAccounts(users, "", nil)

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, as(httptest.NewRequest("GET", "/admin", nil), "doug@example.com"))
	// Whatever /admin falls through to, it is not the account page.
	if strings.Contains(w.Body.String(), "signed in as") {
		t.Error("the admin page is served with no admin configured")
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
