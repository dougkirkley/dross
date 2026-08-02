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

// While nobody can approve anyone, the application itself has to load - it is
// what tells the person what is going on and offers them the role. Only the
// page: an unapproved account is still unapproved, so anything that is not a
// browser navigation is still refused.
func TestFirstRunLetsThePageLoadButNotTheAPI(t *testing.T) {
	a := &Authenticator{cfg: Config{}, cookieKey: []byte("test-key")}
	a.SetApproval(fakeApprover{}) // nobody is approved
	a.SetNeedsSetup(func() bool { return true }, "/setup")

	reached := false
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
	}))

	signedIn := func(accept string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: a.signSession("first@example.com")})
		if accept != "" {
			r.Header.Set("Accept", accept)
		}
		return r
	}

	reached = false
	w := httptest.NewRecorder()
	h.ServeHTTP(w, signedIn("text/html"))
	if !reached || w.Code != http.StatusOK {
		t.Errorf("the app did not load during first run: code=%d reached=%v", w.Code, reached)
	}

	// An asset the page needs to run. It asks with Accept: */*, so keying the
	// exemption on text/html let the document load and refused everything it
	// depended on - a blank page with nothing to report. Assets go through.
	reached = false
	w = httptest.NewRecorder()
	asset := httptest.NewRequest("GET", "/main.dart.js", nil)
	asset.AddCookie(&http.Cookie{Name: sessionCookie, Value: a.signSession("first@example.com")})
	asset.Header.Set("Accept", "*/*")
	h.ServeHTTP(w, asset)
	if !reached || w.Code != http.StatusOK {
		t.Errorf("the app could not load its own bundle during first run: code=%d reached=%v", w.Code, reached)
	}

	// The API does not, whatever it asks for.
	for _, accept := range []string{"application/json", "text/html", "*/*"} {
		reached = false
		w = httptest.NewRecorder()
		api := httptest.NewRequest("GET", "/api/sessions", nil)
		api.AddCookie(&http.Cookie{Name: sessionCookie, Value: a.signSession("first@example.com")})
		api.Header.Set("Accept", accept)
		h.ServeHTTP(w, api)
		if reached || w.Code != http.StatusForbidden {
			t.Errorf("an API call with Accept %q got through during first run: code=%d reached=%v",
				accept, w.Code, reached)
		}
	}
}

// The other direction, through the middleware itself: once somebody can approve
// people, an unapproved account gets the holding page and not the application.
func TestNoSetupNeededMeansTheHoldingPage(t *testing.T) {
	a := &Authenticator{cfg: Config{}, cookieKey: []byte("test-key")}
	a.SetApproval(fakeApprover{})
	a.SetNeedsSetup(func() bool { return false }, "/setup") // an admin exists

	reached := false
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached = true }))

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: a.signSession("waiting@example.com")})
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if reached {
		t.Fatal("an unapproved account reached the application while an admin existed")
	}
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "Request received") {
		t.Errorf("expected the holding page, got %d: %.120s", w.Code, w.Body.String())
	}
}
