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
		StaticToken:   "sekret",
		AllowedEmails: emails,
		CookieSecret:  []byte("0123456789abcdef0123456789abcdef"),
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

// TestStaticTokenBypassesAllowlist proves the static token authenticates and is
// exempt from the email allowlist, while an unknown OIDC email would not be.
func TestStaticTokenBypassesAllowlist(t *testing.T) {
	a := testAuth(t, "only@allowed.com") // allowlist set

	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(Identity(r)))
	}))

	// Right token → 200 even though "api-token" is not in the allowlist.
	r := httptest.NewRequest("GET", "/api/sessions", nil)
	r.Header.Set("Authorization", "Bearer sekret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "api-token" {
		t.Fatalf("static token: code=%d body=%q", w.Code, w.Body.String())
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
