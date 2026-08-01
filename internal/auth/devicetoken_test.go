package auth

import (
	"net/http/httptest"
	"testing"
	"time"
)

// A session pod runs model-chosen code, so its credential must be usable for
// exactly one thing: attaching as its own device. Never as the user.
func TestDeviceTokenIsScopedAndNotALogin(t *testing.T) {
	a := testAuth(t, "doug@example.com")

	tok, err := a.MintDeviceToken("doug@example.com", "ses_abc", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	email, device, ok := a.VerifyDeviceToken(tok)
	if !ok || email != "doug@example.com" || device != "ses_abc" {
		t.Fatalf("verify = %q/%q ok=%v", email, device, ok)
	}

	// It must not authenticate an API request.
	r := httptest.NewRequest("GET", "/api/sessions", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	if _, _, ok := a.identify(r); ok {
		t.Error("device token authenticated as a user — it must not be a login")
	}

	// Tampering and expiry are refused.
	if _, _, ok := a.VerifyDeviceToken(tok + "x"); ok {
		t.Error("tampered device token accepted")
	}
	expired, _ := a.MintDeviceToken("doug@example.com", "ses_abc", -time.Minute)
	if _, _, ok := a.VerifyDeviceToken(expired); ok {
		t.Error("expired device token accepted")
	}
	// A session token must not pass as a device token, or vice versa.
	if _, _, ok := a.VerifyDeviceToken(a.signSession("doug@example.com")); ok {
		t.Error("a session token was accepted as a device token")
	}
}
