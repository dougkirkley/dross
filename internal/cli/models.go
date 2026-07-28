package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dougkirkley/holocron/internal/config"
	"github.com/dougkirkley/holocron/internal/provider"
)

// leadState is the TUI's mutable lead provider, reconfigurable via /model.
type leadState struct {
	name    string // display kind: ollama / openai / anthropic
	baseURL string
	apiKey  string
	model   string
	prov    provider.Provider
}

// providerImpl maps a user-facing provider kind to a registered provider name.
// "ollama" is OpenAI-compatible, so it uses the openai backend with a base URL.
func providerImpl(kind string) string {
	if kind == "ollama" {
		return "openai"
	}
	return kind
}

func baseNote(base string) string {
	if base == "" {
		return ""
	}
	return " at " + base
}

// saveLeadProvider persists the lead provider to the config file so it sticks
// across sessions. It writes the impl provider name (so provider.New works on
// reload) plus base URL, key, and model, merging into any existing file.
func saveLeadProvider(explicit string, st *leadState) error {
	f, path, err := config.Load(explicit)
	if err != nil {
		f = &config.File{}
	}
	if path == "" {
		dir, derr := os.UserConfigDir()
		if derr != nil {
			return derr
		}
		path = filepath.Join(dir, "holocron", "config.json")
	}
	f.Provider = providerImpl(st.name)
	f.BaseURL = st.baseURL
	f.APIKey = st.apiKey
	f.Model = st.model
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

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
