// Package config resolves provider settings for the CLI from three layers, in
// increasing precedence: a JSON config file, environment variables, and command
// flags (applied by the caller). The file format mirrors the shape of tools
// like opencode (opencode.json) and Claude (settings.json): a default provider
// plus an optional cheaper worker.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Provider is one backend's settings.
type Provider struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
}

// File is the on-disk config. All fields optional.
type File struct {
	Provider    string      `json:"provider,omitempty"`
	Model       string      `json:"model,omitempty"`
	BaseURL     string      `json:"base_url,omitempty"`
	APIKey      string      `json:"api_key,omitempty"`
	Style       string      `json:"style,omitempty"`
	MaxTokens   int         `json:"max_tokens,omitempty"`
	MaxParallel int         `json:"max_parallel,omitempty"` // parallel worker subagents
	Models      []string    `json:"models,omitempty"`       // allowed model ids to offer
	Worker      *Provider   `json:"worker,omitempty"`
	Reviewer    *Provider   `json:"reviewer,omitempty"`
	Compactor   *Provider   `json:"compactor,omitempty"`   // token-efficiency skill
	CompactAt   int         `json:"compact_at,omitempty"`  // tool-result byte threshold
	Phrases     []string    `json:"phrases,omitempty"` // TUI status phrases
	OAuth       *OAuthLogin `json:"oauth,omitempty"`   // for the claude-sub provider
}

// OAuthLogin configures the `holocron login` PKCE flow and the claude-sub
// provider. Every value is user-supplied — holocron ships no OAuth client and
// impersonates nobody. To use a Claude subscription you must fill these in
// yourself (client id, endpoints, and the header/system-pin that scope needs).
type OAuthLogin struct {
	ClientID     string `json:"client_id"`
	AuthorizeURL string `json:"authorize_url"`
	TokenURL     string `json:"token_url"`
	RedirectURI  string `json:"redirect_uri"`
	Scope        string `json:"scope"`
	ShowCode     bool   `json:"show_code"`   // add ?code=true for a paste-code page
	BetaHeader   string `json:"beta_header"` // anthropic-beta value, if the scope needs one
	SystemPin    string `json:"system_pin"`  // first system block the scope requires
	BaseURL      string `json:"base_url"`    // provider endpoint the token is valid for
}

func (o *OAuthLogin) Configured() bool {
	return o != nil && o.ClientID != "" && o.AuthorizeURL != "" && o.TokenURL != ""
}

// SearchPaths returns the config file locations checked, in order. An explicit
// path (from --config) wins; then $HOLOCRON_CONFIG; then ./holocron.json; then
// the user config dir.
func SearchPaths(explicit string) []string {
	var paths []string
	if explicit != "" {
		paths = append(paths, explicit)
	}
	if env := os.Getenv("HOLOCRON_CONFIG"); env != "" {
		paths = append(paths, env)
	}
	paths = append(paths, "holocron.json")
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".holocron", "settings.json"))
	}
	if dir, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(dir, "holocron", "config.json"))
	}
	return paths
}

// Load reads the first config file that exists among SearchPaths(explicit). It
// returns an empty File (not an error) when none is found, so config is always
// optional. A malformed file is a hard error.
func Load(explicit string) (*File, string, error) {
	for _, p := range SearchPaths(explicit) {
		b, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, "", err
		}
		var f File
		if err := json.Unmarshal(b, &f); err != nil {
			return nil, p, err
		}
		return &f, p, nil
	}
	return &File{}, "", nil
}

// EnvFor returns the base URL and API key implied by environment variables for
// the named provider, matching the vars other tools honor. Credentials for
// bedrock come from the standard AWS chain (handled by the SDK), so only an
// optional base-URL override is read here.
func EnvFor(provider string) (baseURL, apiKey string) {
	switch provider {
	case "anthropic":
		return os.Getenv("ANTHROPIC_BASE_URL"), os.Getenv("ANTHROPIC_API_KEY")
	case "openai":
		base := os.Getenv("OPENAI_BASE_URL")
		if base == "" {
			base = os.Getenv("OPENAI_API_BASE") // older/LangChain-style name
		}
		return base, os.Getenv("OPENAI_API_KEY")
	case "bedrock":
		return os.Getenv("ANTHROPIC_BEDROCK_BASE_URL"), ""
	}
	return "", ""
}

// Resolve merges the three layers for a single provider slot. flagChanged marks
// whether each flag was explicitly set on the command line; when it was, the
// flag value wins outright. Otherwise env wins over the file, which wins over
// the flag's default value.
func Resolve(in Layers) Provider {
	pick := func(flagVal string, changed bool, env, file, def string) string {
		if changed && flagVal != "" {
			return flagVal
		}
		if env != "" {
			return env
		}
		if file != "" {
			return file
		}
		if changed { // changed to empty string explicitly
			return flagVal
		}
		return def
	}

	name := pick(in.ProviderFlag, in.ProviderChanged, "", in.File.Provider, in.ProviderDefault)
	envBase, envKey := EnvFor(name)
	return Provider{
		Provider: name,
		Model:    pick(in.ModelFlag, in.ModelChanged, "", in.File.Model, in.ModelDefault),
		BaseURL:  pick(in.BaseURLFlag, in.BaseURLChanged, envBase, in.File.BaseURL, ""),
		APIKey:   pick(in.APIKeyFlag, in.APIKeyChanged, envKey, in.File.APIKey, ""),
	}
}

// Layers carries the raw inputs for Resolve.
type Layers struct {
	File File

	ProviderFlag, ProviderDefault string
	ProviderChanged               bool
	ModelFlag, ModelDefault       string
	ModelChanged                  bool
	BaseURLFlag                   string
	BaseURLChanged                bool
	APIKeyFlag                    string
	APIKeyChanged                 bool
}
