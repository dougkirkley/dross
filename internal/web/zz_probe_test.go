package web

import (
	"fmt"
	"net/http/httptest"
	"testing"
)

// Prove that the path the web handler resolves is derived from the DECODED
// r.URL.Path, so it can point outside whatever subtree the route was mounted at.
func TestProbeIconsEscape(t *testing.T) {
	h, ok := Handler()
	if !ok {
		t.Fatal("no handler")
	}
	for _, p := range []string{
		"/icons/x.png",
		"/icons/..%2findex.html",
		"/icons/%2e%2e%2findex.html",
		"/icons/..%2f..%2findex.html",
		"/manifest.json",
	} {
		r := httptest.NewRequest("GET", p, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		fmt.Printf("web %-32s escaped=%q decoded=%q -> %d loc=%q len=%d\n",
			p, r.URL.EscapedPath(), r.URL.Path, w.Code, w.Header().Get("Location"), w.Body.Len())
	}
}
