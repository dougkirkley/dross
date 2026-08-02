package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeApprover map[string]bool

func (f fakeApprover) IsApproved(email string) bool { return f[strings.ToLower(email)] }

// The gate is the whole point of the split: Google says who someone is, dross
// says whether they may be here. An authenticated stranger must get no further
// than the holding page.
func TestMiddlewareHoldsUnapprovedAccounts(t *testing.T) {
	a := &Authenticator{
		cfg:       Config{ServiceToken: "svc-secret", ServiceIdentity: "service@dross"},
		cookieKey: []byte("test-key"),
	}
	a.SetApproval(fakeApprover{"doug@example.com": true})

	reached := false
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	signedIn := func(email string, html bool) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: a.signSession(email)})
		if html {
			r.Header.Set("Accept", "text/html")
		}
		return r
	}

	// A browser gets the holding page — a 403 that explains itself, not a
	// bare error and not a redirect loop back to a sign-in they already did.
	w := httptest.NewRecorder()
	reached = false
	h.ServeHTTP(w, signedIn("stranger@example.com", true))
	if w.Code != http.StatusForbidden || reached {
		t.Fatalf("stranger: code=%d reached=%v; want 403 and no handler", w.Code, reached)
	}
	if body := w.Body.String(); !strings.Contains(body, "Request received") ||
		!strings.Contains(body, "stranger@example.com") {
		t.Errorf("holding page missing its copy or the account: %.200s", body)
	}

	// An API client gets a plain 403; a friendly page must not be dressed up
	// as success for something parsing the response.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, signedIn("stranger@example.com", false))
	if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "<html") {
		t.Errorf("api client: code=%d body=%.80s", w.Code, w.Body.String())
	}

	// An approved account passes through.
	w = httptest.NewRecorder()
	reached = false
	h.ServeHTTP(w, signedIn("Doug@Example.com", true))
	if w.Code != http.StatusOK || !reached {
		t.Errorf("approved: code=%d reached=%v; want 200", w.Code, reached)
	}

	// The service token has no user record and must never need one: an
	// automated check would otherwise sit pending for ever.
	w = httptest.NewRecorder()
	reached = false
	r := httptest.NewRequest("GET", "/api/health", nil)
	r.Header.Set("Authorization", "Bearer svc-secret")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !reached {
		t.Errorf("service identity: code=%d reached=%v; want 200", w.Code, reached)
	}
}

// With no approver installed — a local run — every verified identity is allowed.
func TestMiddlewareWithoutApproverAllowsEveryone(t *testing.T) {
	a := &Authenticator{cfg: Config{}, cookieKey: []byte("test-key")}
	ok := false
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { ok = true }))

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: a.signSession("anyone@example.com")})
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !ok {
		t.Error("no approver configured, but the request was refused")
	}
}

// With Kratos configured it must be the one that starts a login: it sets the
// anti-CSRF cookie and mints the flow id, so a page drawn without both can only
// fail on submit. This is a redirect, not dross's own sign-in screen - and the
// patch that first introduced it silently did nothing, which no test noticed.
func TestUnauthenticatedBrowserGoesToKratos(t *testing.T) {
	k, err := NewKratosSessions("http://kratos-public.invalid")
	if err != nil {
		t.Fatal(err)
	}
	a := &Authenticator{cfg: Config{}, cookieKey: []byte("test-key")}
	a.SetKratosSessions(k)
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("an anonymous request reached the application")
	}))

	r := httptest.NewRequest("GET", "/sessions/abc", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("anonymous browser got %d; want a redirect into the Kratos flow", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/kratos/self-service/login/browser") {
		t.Fatalf("redirected to %q; want the Kratos login flow", loc)
	}
	// And it must carry them back to where they were headed.
	if !strings.Contains(loc, "return_to=%2Fsessions%2Fabc") {
		t.Errorf("redirect lost the destination: %q", loc)
	}
}
