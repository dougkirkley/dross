package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOfferable(t *testing.T) {
	allowed := []string{"devstral", "qwen3-coder:30b", "llama3.1:8b"}

	// The point of the whole thing: an allow-list written by hand, narrowed to
	// what is really there.
	got := offerable(allowed, []string{"devstral", "gemma2:9b"})
	if len(got) != 1 || got[0] != "devstral" {
		t.Errorf("got %v, want just the one that is present", got)
	}

	// Order comes from the config, not from whatever order the backend
	// enumerated in — the first entry is the one a picker highlights.
	got = offerable(allowed, []string{"llama3.1:8b", "devstral"})
	if len(got) != 2 || got[0] != "devstral" {
		t.Errorf("got %v, want the configured order preserved", got)
	}

	// Unknown is not none. A backend that could not be reached is a reason to
	// say nothing about its models, not to claim it has none — and an empty
	// picker is a worse failure than a stale one.
	if got := offerable(allowed, nil); len(got) != 3 {
		t.Errorf("got %v, want the list untouched when the backend is unreachable", got)
	}

	// Same argument one step further: if nothing survives, it is far likelier
	// the probe is wrong than that every configured model has vanished.
	if got := offerable(allowed, []string{"something-else"}); len(got) != 3 {
		t.Errorf("got %v, want the list untouched when nothing matches", got)
	}

	// Nothing configured: what the backend has beats the empty picker that
	// used to be shown.
	got = offerable(nil, []string{"devstral", "gemma2:9b"})
	if len(got) != 2 {
		t.Errorf("got %v, want everything the backend reported", got)
	}
}

func TestInstalledReadsTheBackend(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("asked for %q, want the OpenAI-compatible models route", r.URL.Path)
		}
		hits++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"devstral"},{"id":"gemma2:9b"},{"id":""}]}`))
	}))
	defer srv.Close()

	var c installedCache
	got := c.installed(context.Background(), srv.URL+"/v1")
	if len(got) != 2 || got[0] != "devstral" {
		t.Fatalf("got %v, want the two named models", got)
	}
	// Cached: a picker opened twice is one request.
	c.installed(context.Background(), srv.URL+"/v1")
	if hits != 1 {
		t.Errorf("hit the backend %d times, want 1 within the TTL", hits)
	}
}

func TestInstalledIsUnknownOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	var c installedCache
	// nil, not empty: the difference decides whether a caller falls back to the
	// configured list or shows an empty picker.
	if got := c.installed(context.Background(), srv.URL+"/v1"); got != nil {
		t.Errorf("got %v, want nil for a backend that would not answer", got)
	}
	if got := c.installed(context.Background(), ""); got != nil {
		t.Errorf("got %v, want nil when there is no endpoint to ask", got)
	}
}

func TestLoadedModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			t.Errorf("asked for %q, want ollama's running-models route", r.URL.Path)
		}
		w.Write([]byte(`{"models":[{"name":"devstral:latest","model":"devstral"},{"model":"gemma2:9b"}]}`))
	}))
	defer srv.Close()

	// The base URL is the OpenAI-compatible one; /api/ps lives a level up.
	got := loadedModels(context.Background(), srv.URL+"/v1")
	if len(got) != 2 || got[0] != "devstral:latest" || got[1] != "gemma2:9b" {
		t.Errorf("got %v, want both resident models named", got)
	}
}

func TestLoadedModelsIsUnknownOnFailure(t *testing.T) {
	// nil, not empty. "Nothing is loaded" would tell somebody every model costs
	// a cold load; "we could not ask" should say nothing at all.
	if got := loadedModels(context.Background(), ""); got != nil {
		t.Errorf("got %v, want nil with no endpoint", got)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	if got := loadedModels(context.Background(), srv.URL+"/v1"); got != nil {
		t.Errorf("got %v, want nil from a backend that would not answer", got)
	}
}

// ollama reports the tagged form and configs are written with the bare one.
// Compared literally, the default model vanished from its own picker.
func TestOfferableMatchesTheLatestTag(t *testing.T) {
	got := offerable([]string{"devstral", "qwen2.5-coder:14b"},
		[]string{"devstral:latest", "qwen2.5-coder:14b"})
	if len(got) != 2 || got[0] != "devstral" {
		t.Errorf("got %v, want both, spelled the way the config spells them", got)
	}
}
