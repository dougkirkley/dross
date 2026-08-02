package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func testAuth(t *testing.T, emails ...string) *Authenticator {
	t.Helper()
	a, err := New(context.Background(), Config{
		ServiceToken:    "sekret",
		ServiceIdentity: "service@dross",
		AllowedEmails:   emails,
		CookieSecret:    []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil || a == nil {
		t.Fatalf("New: %v (a=%v)", err, a)
	}
	return a
}

// TestSessionCookie proves sign/verify roundtrips, rejects tampering, and
// expires.
func TestSessionCookie(t *testing.T) {
	a := testAuth(t)

	v := a.signSession("me@example.com")
	got, ok := a.readSession(v)
	if !ok || got != "me@example.com" {
		t.Fatalf("roundtrip: %q %v", got, ok)
	}
	// Tamper the payload.
	if _, ok := a.readSession("x" + v); ok {
		t.Fatal("tampered cookie accepted")
	}
	// Wrong key must reject.
	other := testAuth(t)
	other.cookieKey = []byte("ffffffffffffffffffffffffffffffff")
	if _, ok := other.readSession(v); ok {
		t.Fatal("cookie verified under a different key")
	}
	// Expired.
	expired := a.sign("me@example.com|" + strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10))
	if _, ok := a.readSession(expired); ok {
		t.Fatal("expired session accepted")
	}
}

// TestServiceTokenRespectsAllowlist proves the machine credential is an
// identity rather than a bypass: it authenticates, but only when its identity
// is on the allowlist. The old static token skipped that check entirely.
func TestServiceTokenRespectsAllowlist(t *testing.T) {
	a := testAuth(t, "only@allowed.com", "service@dross")

	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(Identity(r)))
	}))

	// Right token, identity allowlisted → 200 as that identity.
	r := httptest.NewRequest("GET", "/api/sessions", nil)
	r.Header.Set("Authorization", "Bearer sekret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "service@dross" {
		t.Fatalf("service token: code=%d body=%q", w.Code, w.Body.String())
	}

	// Same token, identity NOT allowlisted → 403. This is the whole point of
	// replacing the bypassing static token.
	b := testAuth(t, "only@allowed.com")
	hb := b.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rb := httptest.NewRequest("GET", "/api/sessions", nil)
	rb.Header.Set("Authorization", "Bearer sekret")
	wb := httptest.NewRecorder()
	hb.ServeHTTP(wb, rb)
	if wb.Code != http.StatusForbidden {
		t.Fatalf("unlisted service identity: want 403, got %d", wb.Code)
	}

	// No creds → 401.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/sessions", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no creds: want 401, got %d", w.Code)
	}

	// Cookie identity outside the allowlist → 403.
	r = httptest.NewRequest("GET", "/api/sessions", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: a.signSession("stranger@evil.com")})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("stranger: want 403, got %d", w.Code)
	}
}

// TestBearerSessionToken proves a native app can authenticate with the signed
// session token (from the mode=app flow) sent as a bearer, and that the email
// allowlist still applies to it.
func TestBearerSessionToken(t *testing.T) {
	a := testAuth(t, "me@allowed.com")
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(Identity(r)))
	}))

	// Allowlisted session token as bearer → 200.
	r := httptest.NewRequest("GET", "/api/sessions", nil)
	r.Header.Set("Authorization", "Bearer "+a.signSession("me@allowed.com"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "me@allowed.com" {
		t.Fatalf("bearer session: code=%d body=%q", w.Code, w.Body.String())
	}

	// A session for an email off the allowlist → 403 (identity is verified but
	// not permitted).
	r = httptest.NewRequest("GET", "/api/sessions", nil)
	r.Header.Set("Authorization", "Bearer "+a.signSession("stranger@evil.com"))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("stranger bearer: want 403, got %d", w.Code)
	}

	// A garbage bearer → 401.
	r = httptest.NewRequest("GET", "/api/sessions", nil)
	r.Header.Set("Authorization", "Bearer not-a-session")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("garbage bearer: want 401, got %d", w.Code)
	}
}

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"":                 "/",
		"/api/sessions":    "/api/sessions",
		"//evil.com":       "/",
		"https://evil.com": "/",
		"/ok?x=1":          "/ok?x=1",
	}
	for in, want := range cases {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

// Regression: browsers treat "\" as "/" in the authority position, so a next
// parameter of "/\evil.com" navigated off-site after login.
func TestSafeNextRejectsBackslashAuthority(t *testing.T) {
	for _, bad := range []string{`/\evil.com`, `/\/evil.com`, "//evil.com", "https://evil.com",
		"/ok\r\nX-Injected: 1", `\\evil.com`} {
		if got := safeNext(bad); got != "/" {
			t.Errorf("safeNext(%q) = %q; want /", bad, got)
		}
	}
	for _, ok := range []string{"/", "/sessions", "/a/b?c=d"} {
		if got := safeNext(ok); got != ok {
			t.Errorf("safeNext(%q) = %q; want unchanged", ok, got)
		}
	}
}

// A credential must carry the Bearer scheme; TrimPrefix used to accept a bare
// Authorization value as a token.
func TestAuthorizationRequiresBearerScheme(t *testing.T) {
	a := &Authenticator{cfg: Config{ServiceToken: "svc", ServiceIdentity: "service@dross"}}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "svc")
	if _, _, ok := a.identify(r); ok {
		t.Error("bare Authorization value authenticated")
	}
	r.Header.Set("Authorization", "Bearer svc")
	if _, _, ok := a.identify(r); !ok {
		t.Error("Bearer credential rejected")
	}
}

// A service token with no identity would authenticate as the empty string.
func TestServiceTokenRequiresIdentity(t *testing.T) {
	if _, err := New(context.Background(), Config{ServiceToken: "x"}); err == nil {
		t.Fatal("expected an error for a service token without an identity")
	}
}
