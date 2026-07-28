package cli

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
)

// fetchModelIDs lists the models a provider serves, for the TUI /model picker.
// For OpenAI-compatible backends (including ollama) it calls GET {baseURL}/models
// and filters to nothing extra — whatever the server advertises. Anthropic has
// no list endpoint, so a known set is returned.
func fetchModelIDs(provName, baseURL, apiKey string) []string {
	if provName == "anthropic" {
		return []string{
			"claude-opus-4-8", "claude-opus-4-7", "claude-sonnet-4-6", "claude-haiku-4-5",
		}
	}
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	req, err := http.NewRequest(http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	hc := &http.Client{Timeout: 5 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return nil
	}
	ids := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	sort.Strings(ids)
	return ids
}
