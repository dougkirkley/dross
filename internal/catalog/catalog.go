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
	"anthropic": {ID: "anthropic", Name: "Anthropic (API key)", Impl: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY", Models: []string{"claude-opus-4-8", "claude-sonnet-4-6", "claude-haiku-4-5"}},
	// Claude subscription via the local `claude` CLI — no API key. Distinct from
	// the API-key "anthropic" provider above.
	"claude-code": {ID: "claude-code", Name: "Claude Code (subscription)", Impl: "claude-code", Local: true, Models: []string{"opus", "sonnet", "haiku"}},
	// Claude on Amazon Bedrock (credentials/region from the AWS default chain).
	// Model ids are region inference-profile ids; edit for your region.
	// Claude uses the anthropic-over-bedrock path; other ids use the Converse API.
	// Model ids are region inference-profile ids; edit for your region.
	"bedrock": {ID: "bedrock", Name: "Amazon Bedrock", Impl: "bedrock", Models: []string{"us.anthropic.claude-sonnet-4-5-20250929-v1:0", "us.anthropic.claude-haiku-4-5-20251001-v1:0", "us.amazon.nova-pro-v1:0", "us.amazon.nova-lite-v1:0", "us.meta.llama3-3-70b-instruct-v1:0", "mistral.mistral-large-2407-v1:0"}},
	"gemini":      {ID: "gemini", Name: "Google Gemini", Impl: "gemini", APIKeyEnv: "GEMINI_API_KEY", Models: []string{"gemini-3-pro", "gemini-3-flash", "gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite", "gemini-2.0-flash"}},
	// Gemini on Vertex AI (GCP ADC auth; set VERTEX_PROJECT + VERTEX_REGION).
	"vertex": {ID: "vertex", Name: "Google Vertex AI", Impl: "vertex", Models: []string{"gemini-3-pro", "gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.0-flash"}},
	"openai":      {ID: "openai", Name: "OpenAI", Impl: "openai", BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY", Models: []string{"gpt-5", "gpt-5-mini", "o4-mini"}},
	// Azure OpenAI: endpoint via AZURE_OPENAI_ENDPOINT; model = deployment name.
	"azure": {ID: "azure", Name: "Azure OpenAI", Impl: "azure", APIKeyEnv: "AZURE_OPENAI_API_KEY", Models: []string{"gpt-4o", "gpt-4o-mini", "o4-mini"}},

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
	"zai":        {ID: "zai", Name: "Z.ai (GLM)", Impl: "openai", BaseURL: "https://api.z.ai/api/paas/v4", APIKeyEnv: "ZAI_API_KEY", Models: []string{"glm-4.6", "glm-4.5", "glm-4.5-air"}},
	"dashscope":  {ID: "dashscope", Name: "Alibaba Qwen (DashScope)", Impl: "openai", BaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", APIKeyEnv: "DASHSCOPE_API_KEY", Models: []string{"qwen3-coder-plus", "qwen-max", "qwen-plus"}},
	"github":     {ID: "github", Name: "GitHub Models", Impl: "openai", BaseURL: "https://models.github.ai/inference", APIKeyEnv: "GITHUB_TOKEN", Models: []string{"openai/gpt-4o", "openai/gpt-4o-mini", "meta/Llama-3.3-70B-Instruct"}},
	"cohere":     {ID: "cohere", Name: "Cohere (Command)", Impl: "openai", BaseURL: "https://api.cohere.ai/compatibility/v1", APIKeyEnv: "COHERE_API_KEY", Models: []string{"command-a-03-2025", "command-r-plus-08-2024", "command-r7b-12-2024"}},
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
