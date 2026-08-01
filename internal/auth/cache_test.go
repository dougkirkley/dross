package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A CDN must never store an authenticated response and replay it to anonymous
// clients. Cloudflare did precisely that to the app bundle before this.
func TestAuthenticatedResponsesAreNotShareCacheable(t *testing.T) {
	a := testAuth(t, "service@dross")
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret bundle"))
	}))
	r := httptest.NewRequest("GET", "/main.dart.js", nil)
	r.Header.Set("Authorization", "Bearer sekret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	cc := w.Header().Get("Cache-Control")
	if !strings.Contains(cc, "private") || !strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control = %q; want private + no-store", cc)
	}
	if v := w.Header().Values("Vary"); len(v) == 0 {
		t.Error("Vary should list the credential headers")
	}
}
