// Package web serves the Flutter client (compiled to web) embedded into the
// dross binary, so `dross serve` hosts the UI, the JSON/SSE API, and
// artifact links from one process — no separate static host.
//
// The build output is embedded from internal/web/dist. A committed placeholder
// index.html keeps the package (and tests) building without a Flutter build;
// the Docker image overwrites dist with a real `flutter build web` output.
package web

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

func init() {
	// Go's builtin table lacks a few types the Flutter bundle relies on.
	_ = mime.AddExtensionType(".wasm", "application/wasm")
	_ = mime.AddExtensionType(".js", "text/javascript")
	_ = mime.AddExtensionType(".mjs", "text/javascript")
	_ = mime.AddExtensionType(".json", "application/json")
}

// Handler serves the embedded site. Real files are served directly; any other
// path falls back to index.html so Flutter's client-side routing works. Returns
// (handler, true) when a real build is embedded, (nil, false) when only the
// placeholder is present — callers can then skip mounting it.
func Handler() (http.Handler, bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	fileServer := http.FileServer(http.FS(sub))
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		// index.html always goes through serveIndex so the service-worker
		// teardown is injected, whether it was requested directly or reached as
		// the SPA fallback.
		if p == "index.html" {
			serveIndex(w, r, sub)
			return
		}
		if _, err := fs.Stat(sub, p); err != nil {
			// Not a real asset → serve the SPA entry point.
			serveIndex(w, r, sub)
			return
		}
		// Don't let the browser cache the bootstrap.
		if p == "flutter_service_worker.js" {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		}
		fileServer.ServeHTTP(w, r)
	})
	return h, true
}

// killServiceWorker is injected into index.html. dross is server-backed: an
// offline cache buys nothing, and Flutter's service worker keeps serving a
// stale bundle after a deploy, so users sit on an old build with no way out
// from the client. Stop registering one, and tear down anything a previous
// version installed.
const killServiceWorker = `<script>
(function () {
  if (!('serviceWorker' in navigator)) return;
  // Prevent Flutter's loader from installing a new one (it falls back to
  // fetching the bundle directly when registration fails).
  navigator.serviceWorker.register = function () {
    return Promise.reject(new Error('service worker disabled'));
  };
  navigator.serviceWorker.getRegistrations().then(function (rs) {
    if (!rs.length) return;
    return Promise.all(rs.map(function (r) { return r.unregister(); }))
      .then(function () {
        return window.caches ? caches.keys().then(function (ks) {
          return Promise.all(ks.map(function (k) { return caches.delete(k); }));
        }) : null;
      })
      .then(function () {
        // One reload to pick up the real assets; guarded so it cannot loop.
        if (!sessionStorage.getItem('dross_sw_cleared')) {
          sessionStorage.setItem('dross_sw_cleared', '1');
          location.reload();
        }
      });
  }).catch(function () {});
})();
</script>`

func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	b, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Inject early, so it runs before flutter_bootstrap.js registers a worker.
	html := string(b)
	if i := strings.Index(html, "<head>"); i >= 0 {
		html = html[:i+len("<head>")] + killServiceWorker + html[i+len("<head>"):]
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write([]byte(html))
}
