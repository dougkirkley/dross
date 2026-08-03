package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
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

// warmModel asks ollama to load a model without generating anything.
//
// The first message of a chat is typed by a human, which takes long enough to
// hide most of a small model's load — but only if the load starts when the
// chat opens rather than when send is pressed. A generate request with no
// prompt is ollama's documented way to say "load this and hold it"; the
// server's own keep_alive policy decides for how long.
//
// Best effort in every direction: a backend that is not ollama answers 404 and
// nothing happens, an unreachable one times out quietly, and the person typing
// never waits on this call.
func warmModel(ctx context.Context, baseURL, model string) {
	root := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(baseURL), "/"), "/v1")
	if root == "" || model == "" {
		return
	}
	// Loading can take a minute-plus; the timeout is for the load, not a ping.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	body := strings.NewReader(`{"model":` + strconv.Quote(model) + `}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, root+"/api/generate", body)
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// loadedModels lists what a backend has resident right now.
//
// Which model is warm is the single most useful thing to know before picking
// one, and the most expensive thing to find out by trying: on this hardware
// loading a model that is not resident costs a minute or more, and a server
// that can only hold one unloads whatever was there — for everybody, not just
// the person who changed the setting. A picker that shows this turns an
// invisible cost into a visible one.
//
// nil when the backend cannot be asked, which is not the same as "nothing is
// loaded" and must not be shown as though it were.
func loadedModels(ctx context.Context, baseURL string) []string {
	root := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(baseURL), "/"), "/v1")
	if root == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/api/ps", nil)
	if err != nil {
		return nil
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var out struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil
	}
	list := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		if name := cmpOr(m.Name, m.Model); name != "" {
			list = append(list, name)
		}
	}
	return list
}

// cmpOr returns the first non-empty string. ollama fills one field or the
// other depending on version.
func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
