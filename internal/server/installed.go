package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// What a backend actually has, as opposed to what the config hoped it had.
//
// The allowed-models list is written by hand and goes stale the moment a server
// is repointed or a model is dropped: it named four models once, three of which
// lived on a different server or nowhere at all. Picking one of those was not a
// no-op — it either failed the turn outright or pulled a model onto a machine
// sized for smaller ones. A picker should only offer what will work.
//
// This asks the backend. Every provider dross talks to is either
// OpenAI-compatible or answers the same route, so GET {base_url}/models is the
// one question that works everywhere, unlike ollama's own /api/tags.

// installedTTL is how long an answer is reused. The set changes when somebody
// pulls a model, which is rare; the picker is opened often. Long enough that
// opening it twice is one request, short enough that a pull shows up without a
// restart.
const installedTTL = 60 * time.Second

type installedCache struct {
	mu   sync.Mutex
	at   time.Time
	list []string
}

// installed returns the model ids a backend reports, or nil when it cannot be
// asked. nil is the important case: it means "unknown", never "none", so a
// caller must fall back rather than show an empty list.
func (c *installedCache) installed(ctx context.Context, baseURL string) []string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil
	}
	c.mu.Lock()
	if time.Since(c.at) < installedTTL {
		list := c.list
		c.mu.Unlock()
		return list
	}
	c.mu.Unlock()

	list := fetchInstalled(ctx, base)
	c.mu.Lock()
	// A failed probe is not cached as an answer — the next caller should try
	// again rather than inherit a blank for a minute because one request timed
	// out while the backend was busy loading a model.
	if list != nil {
		c.at, c.list = time.Now(), list
	}
	c.mu.Unlock()
	return list
}

func fetchInstalled(ctx context.Context, base string) []string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
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
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return nil
	}
	out := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	return out
}

// offerable narrows an allow-list to what the backend reports having.
//
// Unknown (a backend that could not be reached, or one that does not answer the
// route) leaves the list alone: an unreachable server is a reason to say
// nothing about its models, not a reason to claim it has none. An allow-list
// that survives nothing is also left alone, because an empty picker is worse
// than a stale one and it is more likely the probe is wrong than that every
// configured model is missing.
func offerable(allowed, present []string) []string {
	if present == nil {
		return allowed
	}
	if len(allowed) == 0 {
		// Nothing configured: what the backend has is a better answer than
		// nothing at all, which is what the picker showed before.
		return present
	}
	have := make(map[string]bool, len(present))
	for _, m := range present {
		have[m] = true
	}
	out := make([]string, 0, len(allowed))
	for _, m := range allowed {
		if have[m] {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return allowed
	}
	return out
}
