// Package catalog is a small registry of known model providers: their base URL,
// API-key env var, and backend implementation. It lets `/model <id>` (and
// config) name a provider like "groq" and have the base URL + key filled in
// automatically, instead of typing them. Most entries are OpenAI-compatible and
// use the "openai" backend with a base URL.
//
// This is the seed of provider parity (see docs/providers-roadmap.md); it can be
// extended or, later, sourced from Catwalk/models.dev. Base URLs are the public
// OpenAI-compatible endpoints for each service.
package catalog

import "sort"

// Provider describes how to reach a model provider.
type Provider struct {
	ID        string   // short id used in /model and config (e.g. "groq")
	Name      string   // display name
	Impl      string   // backend: "openai" (OpenAI-compatible) or "anthropic"
	BaseURL   string   // API base (empty for local defaults / native backends)
	APIKeyEnv string   // env var holding the API key ("" if none, e.g. local)
	Local     bool     // runs locally (no key needed)
	Models    []string // a few well-known model ids (preview; live list wins)
}

// providers is the seed catalog. OpenAI-compatible unless Impl says otherwise.
// Models are indicative previews for the picker; selecting a provider fetches
// the live model list, which is authoritative.
var providers = map[string]Provider{
	// First-party / native backends.
	"anthropic": {ID: "anthropic", Name: "Anthropic", Impl: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY", Models: []string{"claude-opus-4-8", "claude-sonnet-4-6", "claude-haiku-4-5"}},
	"openai":    {ID: "openai", Name: "OpenAI", Impl: "openai", BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY", Models: []string{"gpt-5", "gpt-5-mini", "o4-mini"}},

	// Local, OpenAI-compatible (no key).
	"ollama":   {ID: "ollama", Name: "Ollama", Impl: "openai", BaseURL: "http://localhost:11434/v1", Local: true},
	"lmstudio": {ID: "lmstudio", Name: "LM Studio", Impl: "openai", BaseURL: "http://localhost:1234/v1", Local: true},
	"llamacpp": {ID: "llamacpp", Name: "llama.cpp", Impl: "openai", BaseURL: "http://localhost:8080/v1", Local: true},

	// Hosted, OpenAI-compatible.
	"groq":       {ID: "groq", Name: "Groq", Impl: "openai", BaseURL: "https://api.groq.com/openai/v1", APIKeyEnv: "GROQ_API_KEY", Models: []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant"}},
	"cerebras":   {ID: "cerebras", Name: "Cerebras", Impl: "openai", BaseURL: "https://api.cerebras.ai/v1", APIKeyEnv: "CEREBRAS_API_KEY", Models: []string{"llama-3.3-70b", "qwen-3-coder"}},
	"deepseek":   {ID: "deepseek", Name: "DeepSeek", Impl: "openai", BaseURL: "https://api.deepseek.com/v1", APIKeyEnv: "DEEPSEEK_API_KEY", Models: []string{"deepseek-chat", "deepseek-reasoner"}},
	"xai":        {ID: "xai", Name: "xAI (Grok)", Impl: "openai", BaseURL: "https://api.x.ai/v1", APIKeyEnv: "XAI_API_KEY", Models: []string{"grok-4", "grok-3-mini"}},
	"mistral":    {ID: "mistral", Name: "Mistral", Impl: "openai", BaseURL: "https://api.mistral.ai/v1", APIKeyEnv: "MISTRAL_API_KEY", Models: []string{"mistral-large-latest", "codestral-latest"}},
	"openrouter": {ID: "openrouter", Name: "OpenRouter", Impl: "openai", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OPENROUTER_API_KEY", Models: []string{"anthropic/claude-opus-4.8", "openai/gpt-5"}},
	"together":   {ID: "together", Name: "Together", Impl: "openai", BaseURL: "https://api.together.xyz/v1", APIKeyEnv: "TOGETHER_API_KEY", Models: []string{"meta-llama/Llama-3.3-70B-Instruct-Turbo"}},
	"fireworks":  {ID: "fireworks", Name: "Fireworks", Impl: "openai", BaseURL: "https://api.fireworks.ai/inference/v1", APIKeyEnv: "FIREWORKS_API_KEY", Models: []string{"accounts/fireworks/models/qwen3-coder"}},
	"deepinfra":  {ID: "deepinfra", Name: "DeepInfra", Impl: "openai", BaseURL: "https://api.deepinfra.com/v1/openai", APIKeyEnv: "DEEPINFRA_API_KEY", Models: []string{"meta-llama/Llama-3.3-70B-Instruct"}},
	"perplexity": {ID: "perplexity", Name: "Perplexity", Impl: "openai", BaseURL: "https://api.perplexity.ai", APIKeyEnv: "PERPLEXITY_API_KEY", Models: []string{"sonar", "sonar-pro"}},
	"nebius":     {ID: "nebius", Name: "Nebius", Impl: "openai", BaseURL: "https://api.studio.nebius.ai/v1", APIKeyEnv: "NEBIUS_API_KEY"},
	"hyperbolic": {ID: "hyperbolic", Name: "Hyperbolic", Impl: "openai", BaseURL: "https://api.hyperbolic.xyz/v1", APIKeyEnv: "HYPERBOLIC_API_KEY"},
	"moonshot":   {ID: "moonshot", Name: "Moonshot (Kimi)", Impl: "openai", BaseURL: "https://api.moonshot.ai/v1", APIKeyEnv: "MOONSHOT_API_KEY", Models: []string{"kimi-k2", "moonshot-v1-128k"}},
}

// Get returns the catalog entry for id (case-sensitive), if any.
func Get(id string) (Provider, bool) {
	p, ok := providers[id]
	return p, ok
}

// IDs returns all provider ids, sorted.
func IDs() []string {
	out := make([]string, 0, len(providers))
	for id := range providers {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
