package server

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dougkirkley/dross/internal/auth"
	"github.com/dougkirkley/dross/internal/storage"
)

// A session pod's credential must open the relay for its own device and nothing
// else — not the API, and not another session's relay.
func TestDeviceTokenOpensOnlyItsOwnRelay(t *testing.T) {
	a, err := auth.New(context.Background(), auth.Config{
		ServiceToken: "svc", ServiceIdentity: "service@dross",
		CookieSecret: []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(&fakeProvider{}, nil, "m", t.TempDir())
	srv := New(storage.NewSessionStore(t.TempDir()), mgr, a, nil)

	tok, err := a.MintDeviceToken("doug@example.com", "ses_abc", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	// Not a login: the API stays closed to it.
	r := httptest.NewRequest("GET", "/api/sessions", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Errorf("device token reached the API: %d", w.Code)
	}

	// It does reach the relay endpoint (a non-WebSocket request gets past auth
	// and fails on the upgrade, which is the point: auth did not reject it).
	r = httptest.NewRequest("GET", "/api/device/connect", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code == 401 || w.Code == 403 {
		t.Errorf("device token was refused by the relay: %d", w.Code)
	}

	// A garbage device token is refused outright.
	r = httptest.NewRequest("GET", "/api/device/connect", nil)
	r.Header.Set("Authorization", "Bearer dv1.bogus")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Errorf("invalid device token accepted: %d", w.Code)
	}
}
