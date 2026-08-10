package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLocalArtifactsServesWhatItStores: the loopback half. A link is only worth
// returning if fetching it gives back the file.
func TestLocalArtifactsServesWhatItStores(t *testing.T) {
	l := newLocalArtifacts()
	defer l.Close()

	url, err := l.Publish("mockup.html", []byte("<h1>hi</h1>"), "text/html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("artifacts must be served on loopback, got %q", url)
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "<h1>hi</h1>" {
		t.Fatalf("served %q", body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content type %q — a browser will not render it", ct)
	}
	// Named, so a save-as lands on something recognisable rather than the id.
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "mockup.html") {
		t.Errorf("content-disposition %q", cd)
	}
	if l.Published() != 1 {
		t.Errorf("Published() = %d", l.Published())
	}
}

// Nothing listens until something is published. A port bound by every session
// that never publishes is a background listener nobody asked for.
func TestLocalArtifactsIsLazy(t *testing.T) {
	l := newLocalArtifacts()
	defer l.Close()
	if l.Base() != "" {
		t.Fatalf("a listener came up before anything was published: %s", l.Base())
	}
	if l.Published() != 0 {
		t.Error("nothing has been published yet")
	}
}

// TestRemoteControlPublishesToTheServer is the routing decision that matters.
//
// A session driven from a phone runs on this machine and publishes here too,
// so without this the operator is handed a link to loopback on a laptop they
// are not sitting at.
func TestRemoteControlPublishesToTheServer(t *testing.T) {
	var gotName, gotAuth, gotBody, gotMime string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotName = r.Header.Get("X-Artifact-Filename")
		gotAuth = r.Header.Get("Authorization")
		gotMime = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://dross.sh/a/abc123"}`))
	}))
	defer srv.Close()

	local := newLocalArtifacts()
	defer local.Close()
	linked := true
	pub := publisherFor(local, func() bool { return linked },
		serverPublisher(srv.URL, func() string { return "tok-123" }))

	url, err := pub("mockup.html", []byte("<h1>remote</h1>"), "text/html")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://dross.sh/a/abc123" {
		t.Fatalf("linked session got %q, want the server's url", url)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("auth header %q — the /rc token should be reused", gotAuth)
	}
	if gotName != "mockup.html" || gotBody != "<h1>remote</h1>" || !strings.HasPrefix(gotMime, "text/html") {
		t.Errorf("upload was name=%q mime=%q body=%q", gotName, gotMime, gotBody)
	}
	// Nothing was served locally: the point is that it went up.
	if local.Published() != 0 {
		t.Error("a linked session should not also publish to loopback")
	}

	// Unlink, and it goes back to loopback — the route is decided per publish,
	// because /rc can be turned on part-way through a session.
	linked = false
	url, err = pub("again.html", []byte("<h1>local</h1>"), "text/html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("unlinked session got %q, want loopback", url)
	}
}

// A linked session whose upload fails must say so rather than quietly handing
// back a loopback link the operator cannot open.
func TestRemotePublishFailureIsNotSilentlyLocal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	local := newLocalArtifacts()
	defer local.Close()
	pub := publisherFor(local, func() bool { return true },
		serverPublisher(srv.URL, func() string { return "tok" }))

	if _, err := pub("x.html", []byte("x"), "text/html"); err == nil {
		t.Fatal("a refused upload should surface, not fall back to a link that is no use")
	}
}
