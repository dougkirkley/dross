package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The check has to be certain before it says anything. A warning that fires
// when the answer is unknown is a warning people learn to ignore.
func TestCheckModelOnlySpeaksWhenItKnows(t *testing.T) {
	loaded := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			t.Errorf("asked %q; ollama answers this at /api/ps", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen3-coder:30b","model":"qwen3-coder:30b"}]}`))
	}))
	defer loaded.Close()

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer empty.Close()

	// Anything that is not ollama: the endpoint exists and has never heard of
	// /api/ps. Silence is the only correct answer.
	notOllama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer notOllama.Close()

	ctx := context.Background()
	cases := []struct {
		name string
		base string
		want modelState
	}{
		{"loaded", loaded.URL + "/v1", modelResident},
		{"loaded, no /v1 suffix", loaded.URL, modelResident},
		{"not loaded", empty.URL + "/v1", modelCold},
		{"not ollama", notOllama.URL + "/v1", modelUnknown},
		{"unreachable", "http://127.0.0.1:1/v1", modelUnknown},
		{"no endpoint configured", "", modelUnknown},
	}
	for _, c := range cases {
		if got := checkModel(ctx, c.base, "qwen3-coder:30b"); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}

	// A model ollama has but has not loaded is cold, not unknown.
	if got := checkModel(ctx, loaded.URL+"/v1", "some-other-model"); got != modelCold {
		t.Errorf("a model that is not in the loaded list should be cold, got %v", got)
	}
}
