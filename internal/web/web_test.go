package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndex(t *testing.T) {
	h, ok := Handler()
	if !ok {
		t.Fatal("expected an embedded build (placeholder) to be present")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET / = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("index Content-Type = %q, want text/html", ct)
	}
}

func TestHandlerSPAFallback(t *testing.T) {
	h, _ := Handler()
	rr := httptest.NewRecorder()
	// An unknown client-side route must fall back to index.html, not 404.
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/chats/abc", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("SPA fallback = %d, want 200", rr.Code)
	}
	if !strings.HasPrefix(rr.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("fallback should serve html")
	}
}
