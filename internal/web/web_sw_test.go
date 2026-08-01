package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The teardown must be present and run before the Flutter loader, or a stale
// service worker keeps serving an old bundle after every deploy.
func TestIndexInjectsServiceWorkerTeardown(t *testing.T) {
	h, ok := Handler()
	if !ok {
		t.Skip("no embedded build")
	}
	for _, path := range []string{"/", "/index.html", "/some/spa/route"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		body := w.Body.String()
		if !strings.Contains(body, "service worker disabled") {
			t.Errorf("%s: teardown script missing", path)
		}
		sw := strings.Index(body, "service worker disabled")
		boot := strings.Index(body, "flutter_bootstrap.js")
		if boot >= 0 && sw > boot {
			t.Errorf("%s: teardown must precede the Flutter loader", path)
		}
		if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
			t.Errorf("%s: index must not be cached, got %q", path, cc)
		}
	}
}
