package auth

import (
	"net/http/httptest"
	"testing"
)

// The service token must behave like an identity, not a bypass: it authenticates
// as a named principal and the allowlist still decides whether that principal is
// allowed in.
func TestServiceTokenIsAllowlisted(t *testing.T) {
	a := &Authenticator{cfg: Config{
		ServiceToken:    "svc-secret",
		ServiceIdentity: "service@dross",
	}}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer svc-secret")
	email, exempt, ok := a.identify(r)
	if !ok || email != "service@dross" {
		t.Fatalf("identify = %q, ok=%v; want service@dross", email, ok)
	}
	if exempt {
		t.Error("service token must NOT be exempt from the allowlist")
	}

	// Wrong token is refused.
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.Header.Set("Authorization", "Bearer nope")
	if _, _, ok := a.identify(r2); ok {
		t.Error("wrong token accepted")
	}

	// An empty configured token must never match an empty header.
	a3 := &Authenticator{cfg: Config{ServiceIdentity: "service@dross"}}
	r3 := httptest.NewRequest("GET", "/", nil)
	r3.Header.Set("Authorization", "Bearer ")
	if _, _, ok := a3.identify(r3); ok {
		t.Error("unset service token accepted a request")
	}
}
