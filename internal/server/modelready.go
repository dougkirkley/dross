package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Whether the model behind a session is loaded and ready to answer.
//
// A local model that is not resident has to be read off disk and pushed onto a
// GPU before it can produce a token — nearly three minutes for a 30B model on
// this hardware, during which a run looks exactly like one that has hung. The
// session already says something after a while, which turns "is it broken" into
// "it is slow"; this says it at the start, and says which of the two it is.
//
// Only ollama can answer this. There is no such concept in the OpenAI API and
// none of the hosted providers need one, so a provider that does not respond to
// the question is left alone rather than guessed about — an unknown answer must
// never produce a warning, because a warning nobody can act on trains people to
// ignore the ones they can.

// modelState is what we could learn about a model's readiness.
type modelState int

const (
	modelUnknown  modelState = iota // not ollama, or it did not answer
	modelResident                   // loaded and warm
	modelCold                       // known to ollama, not currently loaded
)

// checkModel asks ollama which models are loaded right now.
//
// baseURL is the OpenAI-compatible endpoint the provider was configured with;
// ollama serves its own API alongside, one level up from /v1.
func checkModel(ctx context.Context, baseURL, model string) modelState {
	root := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(baseURL), "/"), "/v1")
	if root == "" || model == "" {
		return modelUnknown
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/api/ps", nil)
	if err != nil {
		return modelUnknown
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		// Unreachable is not the same as cold, and saying "your model is
		// loading" when the endpoint is refusing connections would send someone
		// looking in the wrong place. The run will fail with a real error in a
		// moment and that error is the honest one.
		return modelUnknown
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return modelUnknown // not ollama, or a version without /api/ps
	}
	var out struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return modelUnknown
	}
	for _, m := range out.Models {
		// ollama reports both, and which one carries the tag varies by version.
		if strings.EqualFold(m.Name, model) || strings.EqualFold(m.Model, model) {
			return modelResident
		}
	}
	return modelCold
}
