package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dougkirkley/ergon/internal/catalog"
	"github.com/dougkirkley/ergon/internal/config"
	"github.com/dougkirkley/ergon/pkg/provider"
)

var mentionRE = regexp.MustCompile(`@([^\s@]+)`)

// expandMentions inlines "@path" file references in a prompt: each mention that
// resolves to a readable file inside dir gets its contents appended in a
// <file> block, like Claude Code's @-mentions. Non-files are left as-is.
func expandMentions(dir, prompt string) string {
	var blocks strings.Builder
	seen := map[string]bool{}
	for _, m := range mentionRE.FindAllStringSubmatch(prompt, -1) {
		p := strings.Trim(m[1], ".,);:'\"")
		if p == "" || seen[p] {
			continue
		}
		full := filepath.Clean(filepath.Join(dir, p))
		if rel, err := filepath.Rel(dir, full); err != nil || strings.HasPrefix(rel, "..") {
			continue // stay within the workspace
		}
		data, err := os.ReadFile(full)
		if err != nil || len(data) == 0 {
			continue
		}
		if len(data) > 50*1024 {
			data = data[:50*1024]
		}
		seen[p] = true
		blocks.WriteString("\n\n<file path=\"" + p + "\">\n" + string(data) + "\n</file>")
	}
	if blocks.Len() == 0 {
		return prompt
	}
	return prompt + "\n\n# Referenced files" + blocks.String()
}

// loadProjectContext reads project instruction files (AGENTS.md, CLAUDE.md) from
// the workspace root and returns them concatenated, for the agent's system
// prompt. Empty when none exist.
func loadProjectContext(dir string) string {
	var b strings.Builder
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || len(strings.TrimSpace(string(data))) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("# " + name + "\n" + string(data))
	}
	return b.String()
}

// leadState is the TUI's mutable lead provider, reconfigurable via /model.
type leadState struct {
	name    string // display kind: ollama / openai / anthropic
	baseURL string
	apiKey  string
	model   string
	prov    provider.Provider

	// worker: the cheaper model the lead delegates bounded subtasks to.
	workerName    string
	workerBaseURL string
	workerAPIKey  string
	workerModel   string
	workerProv    provider.Provider
}

// welcomeStyleFor maps a config welcome name to the TUI layout variant.
func welcomeStyleFor(name string) int {
	switch name {
	case "skills":
		return 1
	case "columns":
		return 2
	case "boxed":
		return 3
	case "centered":
		return 4
	default: // "clean" or unset
		return 0
	}
}

// providerImpl maps a user-facing provider kind (which may be a catalog id like
// "groq") to a registered backend name ("openai", "anthropic", …).
func providerImpl(kind string) string {
	if c, ok := catalog.Get(kind); ok {
		return c.Impl
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
		path = filepath.Join(dir, "ergon", "config.json")
	}
	f.Provider = st.name // catalog id / kind; resolved to a backend via providerImpl on load
	f.BaseURL = st.baseURL
	f.APIKey = st.apiKey
	f.Model = st.model
	if st.workerModel != "" && st.workerName != "" {
		f.Worker = &config.Provider{
			Provider: st.workerName, BaseURL: st.workerBaseURL,
			APIKey: st.workerAPIKey, Model: st.workerModel,
		}
	}
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
	if provName == "claude-code" {
		// Claude Code has no list endpoint; offer its model aliases.
		return []string{"opus", "sonnet", "haiku"}
	}
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
