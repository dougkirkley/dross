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

import (
	"net"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
)

// LocalKind names a kind of local model server, when a provider is one.
//
// Not a boolean, because these servers are not interchangeable in what they
// can be asked. Ollama will say what a model is capable of and can be told to
// keep one loaded; llama.cpp answers a single model and knows nothing of the
// idea. Recording which one it is means dross can use what is there instead of
// probing blindly or assuming the least.
//
// Inference itself is the same everywhere — all four speak the
// OpenAI-compatible route, which is why Impl stays "openai". The kind decides
// only what can be asked *besides* inference.
type LocalKind string

const (
	NotLocal LocalKind = ""         // a hosted provider
	Ollama   LocalKind = "ollama"   // /api/show, /api/ps, keep_alive
	LMStudio LocalKind = "lmstudio" // OpenAI-compatible, model list only
	LlamaCPP LocalKind = "llamacpp" // one model, served directly
	LiteLLM  LocalKind = "litellm"  // a proxy in front of anything
)

// LocalKinds are the kinds a provider may declare, for validation and help.
func LocalKinds() []LocalKind {
	return []LocalKind{Ollama, LMStudio, LlamaCPP, LiteLLM}
}

// ValidLocalKind reports whether s names a local server dross knows.
func ValidLocalKind(s string) bool {
	for _, k := range LocalKinds() {
		if string(k) == s {
			return true
		}
	}
	return false
}

// Provider describes how to reach a model provider.
type Provider struct {
	ID        string // short id used in /model and config (e.g. "groq")
	Name      string // display name
	Impl      string // backend: "openai" (OpenAI-compatible) or "anthropic"
	BaseURL   string // API base (empty for local defaults / native backends)
	APIKeyEnv string // env var holding the API key ("" if none, e.g. local)
	// Local names the kind of local server, or is empty for a hosted provider.
	// A local provider needs no API key.
	Local  LocalKind
	Models []string // a few well-known model ids (preview; live list wins)
	// Key is a literal API key, set only by a user-defined provider. The
	// built-in catalog never carries one — it names the environment variable
	// instead, so the compiled binary holds no credentials.
	Key string
}

// IsLocal reports whether this provider is a local model server.
func (p Provider) IsLocal() bool { return p.Local != NotLocal }

// APIKey resolves the provider's key: the literal one if configured, otherwise
// whatever its named environment variable holds.
func (p Provider) APIKey() string {
	if p.Key != "" {
		return p.Key
	}
	if p.APIKeyEnv != "" {
		return os.Getenv(p.APIKeyEnv)
	}
	return ""
}

// providers is the seed catalog. OpenAI-compatible unless Impl says otherwise.
// Models are indicative previews for the picker; selecting a provider fetches
// the live model list, which is authoritative.
var providers = map[string]Provider{
	// First-party / native backends.
	"anthropic": {ID: "anthropic", Name: "Anthropic (API key)", Impl: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY", Models: []string{"claude-opus-4-8", "claude-sonnet-4-6", "claude-haiku-4-5"}},
	// Claude subscription via the local `claude` CLI — no API key. Distinct from
	// the API-key "anthropic" provider above.
	"claude-code": {ID: "claude-code", Name: "Claude Code (subscription)", Impl: "claude-code", Models: []string{"opus", "sonnet", "haiku"}},
	// Claude on Amazon Bedrock (credentials/region from the AWS default chain).
	// Model ids are region inference-profile ids; edit for your region.
	// Claude uses the anthropic-over-bedrock path; other ids use the Converse API.
	// Model ids are region inference-profile ids; edit for your region.
	"bedrock": {ID: "bedrock", Name: "Amazon Bedrock", Impl: "bedrock", Models: []string{"us.anthropic.claude-sonnet-4-5-20250929-v1:0", "us.anthropic.claude-haiku-4-5-20251001-v1:0", "us.amazon.nova-pro-v1:0", "us.amazon.nova-lite-v1:0", "us.meta.llama3-3-70b-instruct-v1:0", "mistral.mistral-large-2407-v1:0"}},
	"gemini":  {ID: "gemini", Name: "Google Gemini", Impl: "gemini", APIKeyEnv: "GEMINI_API_KEY", Models: []string{"gemini-3-pro-preview", "gemini-3-flash-preview", "gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite", "gemini-2.0-flash"}},
	// Gemini on Vertex AI (GCP ADC auth; set VERTEX_PROJECT + VERTEX_REGION).
	"vertex": {ID: "vertex", Name: "Google Vertex AI", Impl: "vertex", Models: []string{"gemini-3-pro-preview", "gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.0-flash"}},
	"openai": {ID: "openai", Name: "OpenAI", Impl: "openai", BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY", Models: []string{"gpt-5", "gpt-5-mini", "o4-mini"}},
	// Azure OpenAI: endpoint via AZURE_OPENAI_ENDPOINT; model = deployment name.
	"azure": {ID: "azure", Name: "Azure OpenAI", Impl: "azure", APIKeyEnv: "AZURE_OPENAI_API_KEY", Models: []string{"gpt-4o", "gpt-4o-mini", "o4-mini"}},

	// Local servers, OpenAI-compatible (no key). Each declares its kind, which
	// is what dross can ask it beyond inference.
	"ollama":   {ID: "ollama", Name: "Ollama", Impl: "openai", BaseURL: "http://localhost:11434/v1", Local: Ollama},
	"lmstudio": {ID: "lmstudio", Name: "LM Studio", Impl: "openai", BaseURL: "http://localhost:1234/v1", Local: LMStudio},
	"llamacpp": {ID: "llamacpp", Name: "llama.cpp", Impl: "openai", BaseURL: "http://localhost:8080/v1", Local: LlamaCPP},
	"litellm":  {ID: "litellm", Name: "LiteLLM", Impl: "openai", BaseURL: "http://localhost:4000/v1", Local: LiteLLM},

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

// User-defined providers, layered over the built-ins.
//
// The compiled catalog is a convenience for services everyone can reach; it
// can never name a private endpoint. Somebody running a model server of their
// own could point an existing entry at it, but not call it what it is, and a
// provider that is not in here is one the picker cannot show and /model cannot
// resolve by name.
var (
	userMu        sync.RWMutex
	userProviders map[string]Provider
)

// SetUserProviders registers providers from config over the built-in catalog.
//
// An id that already exists is merged rather than replaced, field by field, so
// redirecting one thing about a known provider — usually the base URL — does
// not mean restating its name, backend and key variable to keep them.
//
// Called once at startup, before anything reads the catalog.
func SetUserProviders(ps map[string]Provider) {
	merged := make(map[string]Provider, len(ps))
	for id, p := range ps {
		if id == "" {
			continue
		}
		p.ID = id
		if base, ok := providers[id]; ok {
			if p.Name == "" {
				p.Name = base.Name
			}
			if p.Impl == "" {
				p.Impl = base.Impl
			}
			if p.BaseURL == "" {
				p.BaseURL = base.BaseURL
			}
			if p.APIKeyEnv == "" {
				p.APIKeyEnv = base.APIKeyEnv
			}
			if p.Key == "" {
				p.Key = base.Key
			}
			if len(p.Models) == 0 {
				p.Models = base.Models
			}
			if p.Local == NotLocal {
				p.Local = base.Local
			}
		}
		if p.Name == "" {
			p.Name = id
		}
		if p.Impl == "" {
			p.Impl = "openai" // the compatible default; what most endpoints are
		}
		merged[id] = p
	}
	userMu.Lock()
	userProviders = merged
	userMu.Unlock()
}

// Get returns the catalog entry for id (case-sensitive), if any. A
// user-defined provider wins over a built-in of the same id.
func Get(id string) (Provider, bool) {
	userMu.RLock()
	p, ok := userProviders[id]
	userMu.RUnlock()
	if !ok {
		p, ok = providers[id]
	}
	if ok && id == "ollama" && p.BaseURL == providers["ollama"].BaseURL {
		// Only when nothing more specific was configured: an explicit base URL
		// is a decision, and an environment variable should not overrule it.
		if base := ollamaHost(); base != "" {
			p.BaseURL = base
		}
	}
	return p, ok
}

// ollamaHost resolves $OLLAMA_HOST to an OpenAI-compatible base URL.
//
// The catalog default is localhost, which is right for the common case and
// wrong for every ollama that runs somewhere else — and a provider pointed at
// a host with nothing on it lists no models, which is how a perfectly healthy
// remote server came to be missing from the picker entirely.
//
// OLLAMA_HOST is ollama's own variable, so anyone with a remote server has
// already set it. It accepts a bare host, a host:port, or a full URL; all
// three arrive here and leave as "<scheme>://<host>:<port>/v1".
func ollamaHost() string {
	h := strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
	if h == "" {
		return ""
	}
	// Three shapes, and the port is what separates them.
	//
	// A full URL is taken as written: its default port is its scheme's.
	// Forcing 11434 here turned a working https:// address into a dead one,
	// because a server published through a gateway answers on 443 and has no
	// 11434 open to the world at all.
	//
	// A bare host with a port is the local convention — "localhost:11434",
	// "node2:11434" — and speaks plain http.
	//
	// A bare host without one is a published name, and a published name is
	// https. Defaulting those to http://host:11434 was how a perfectly healthy
	// server stayed invisible: the address was reachable, just not the one
	// being asked.
	if !strings.Contains(h, "://") {
		if _, _, err := net.SplitHostPort(h); err == nil {
			h = "http://" + h
		} else {
			h = "https://" + h
		}
	}
	u, err := url.Parse(h)
	if err != nil || u.Host == "" {
		return ""
	}
	u.Path, u.RawQuery, u.Fragment = "/v1", "", ""
	return u.String()
}

// IDs returns all provider ids, built-in and user-defined, sorted.
func IDs() []string {
	seen := make(map[string]bool, len(providers))
	out := make([]string, 0, len(providers))
	for id := range providers {
		seen[id] = true
		out = append(out, id)
	}
	userMu.RLock()
	for id := range userProviders {
		if !seen[id] {
			out = append(out, id)
		}
	}
	userMu.RUnlock()
	sort.Strings(out)
	return out
}
