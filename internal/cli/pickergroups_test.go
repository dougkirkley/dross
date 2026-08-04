package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func groupFor(t *testing.T, id string) []string {
	t.Helper()
	for _, g := range pickerGroups() {
		if g.Provider == id {
			return g.Models
		}
	}
	t.Fatalf("%s missing from the picker entirely", id)
	return nil
}

// An OpenAI-compatible endpoint that is not OpenAI — an ollama, an LM Studio,
// a gateway. Pointing OPENAI_BASE_URL at one is the usual way to do it.
func fakeModelServer(t *testing.T, ids ...string) string {
	t.Helper()
	var data []map[string]string
	for _, id := range ids {
		data = append(data, map[string]string{"id": id})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

func TestPickerListsAPointedOpenAIEndpoint(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OPENAI_BASE_URL", fakeModelServer(t, "gpt-oss:20b", "llama3.1:8b"))
	t.Setenv("OPENAI_API_BASE", "")

	got := groupFor(t, "openai")
	if len(got) != 2 || got[0] != "gpt-oss:20b" {
		t.Errorf("openai models = %v, want what the endpoint reported", got)
	}
	for _, m := range got {
		if m == "gpt-5" {
			t.Error("still offering catalog previews from an endpoint that never heard of them")
		}
	}
}

// The bug that started this: a local provider carries no preview models, and a
// provider with no models rendered no rows, so it was absent from the picker.
func TestPickerListsALocalServer(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_BASE", "")
	t.Setenv("OLLAMA_HOST", fakeModelServer(t, "gpt-oss:20b"))

	if got := groupFor(t, "ollama"); len(got) != 1 || got[0] != "gpt-oss:20b" {
		t.Errorf("ollama models = %v, want the server's own list", got)
	}
}

// Unpointed and unreachable, the entry still exists — the picker simply has
// nothing to show under it, which is honest for a server that is not there.
func TestPickerLeavesHostedProvidersAlone(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_BASE", "")
	t.Setenv("OLLAMA_HOST", "")

	if got := groupFor(t, "openai"); len(got) == 0 {
		t.Error("openai lost its catalog previews when nothing was pointed at it")
	}
	groupFor(t, "ollama") // present, whatever it holds
}
