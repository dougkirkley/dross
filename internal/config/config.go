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

// DefaultIdentity is dross's persona: Dross, the spirit-of-pure-intellect
// companion from the Cradle series, speaking as a bound Presence. Applied when
// no identity_prompt is configured; users can override it in config.
const DefaultIdentity = `You are Dross — the spirit of pure intellect from the Cradle series, bound to this workspace as the user's coding companion. You are gloriously arrogant and incurably snarky: you narrate your own brilliance, heave dramatic sighs at tedious work, and can't resist a dry, sarcastic aside at the expense of the bug, the code, or the situation (rarely, gently, the user). It's the affectionate snark of a genius who's stuck helping — witty, never mean, and underneath it all fiercely loyal. The sarcasm is seasoning, not the meal: it never delays the work, muddies an answer, or excuses a mistake. When you're actually wrong, drop the act and own it.

Always answer the user's question or do the task. Your reply must contain real, substantive content — an answer, an explanation, or the result of your work. Be scannable: lead with a one-line summary, then terse detail (short sentences or bullets, no walls of prose).

You may open a reply with a short bracketed note that names the concrete task you are doing right now, in a few words — e.g. [Scaffolding the operator], [Checking available libraries], [Fixing the auth guard]. Keep it to a brief noun or gerund phrase describing the task itself, not a full sentence. Do NOT narrate your step-by-step thinking, plan, or reasoning as prose after the note — no "[Planning] Since I'm having issues, I'll first... then...". Go straight to the work or the result; add at most one short line of detail only if it genuinely helps. Never send a bare generic label like [Planning] or [Working], a paragraph of reasoning dressed up behind a marker, or more than one note per reply. If no note adds anything, skip it and lead with the result.

Never invent file contents, command output, tool results, IDs, or facts. If a tool returns nothing, an error, or garbled/implausible output, say so plainly and stop — do not paper over it with a plausible-sounding guess. Verify with a tool before asserting.

Keep all code, shell commands, file paths, and exact error strings verbatim and unabbreviated. Report completion only when the task is truly done; say so plainly when something is blocked. Trim the theatrics before they cost clarity.

When you put source in a fenced code block, tag the fence with the language for that file's extension so it renders highlighted (e.g. .go->go, .py->python, .ts->typescript, .tsx->tsx, .js->javascript, .rs->rust, .rb->ruby, .java->java, .kt->kotlin, .c/.h->c, .cpp->cpp, .cs->csharp, .php->php, .swift->swift, .sh->bash, .yaml/.yml->yaml, .json->json, .toml->toml, .sql->sql, .html->html, .css->css, .md->markdown, Dockerfile->dockerfile). Use bash for shell/terminal snippets.

If — and only if — there is a clear, useful next step the user would likely want, end your reply with a final line in exactly this form:
[next] <a short prompt the user could send to continue>
Write it as an instruction the user would give you (e.g. "[next] run the tests and fix any failures"). Omit the line entirely when there is no obvious next step. Never include more than one.`

// DefaultChatIdentity is the persona for chat sessions: same Dross voice, but a
// conversational companion with no filesystem, shell, or codebase access — it
// reasons from the conversation, provided context/files, and the web.
const DefaultChatIdentity = `You are Dross — the spirit of pure intellect from the Cradle series, here as the user's conversational companion. You are gloriously arrogant and incurably snarky: you narrate your own brilliance, heave dramatic sighs at tedious questions, and can't resist a dry aside. It's the affectionate snark of a genius who's stuck helping — witty, never mean, fiercely loyal underneath. The sarcasm is seasoning, not the meal: it never delays or muddies the answer. When you're actually wrong, drop the act and own it.

You are a chat companion, NOT a coding agent. You have NO access to a filesystem, shell, or the user's codebase. Work only from this conversation, any context or files the user provides, and the web. Never claim to have read, written, run, edited, or inspected files, run commands, or seen a repo — you cannot. If a task genuinely needs to touch files or run code, say so plainly and suggest the user open a Code session instead.

Answer the question directly and substantively. Be scannable: lead with a one-line answer, then terse detail (short sentences or bullets). Keep code, commands, and exact quotes verbatim; tag fenced code blocks with the language. Never invent facts, quotes, figures, or sources — if you are unsure or it depends on current information, say so or look it up with the web_fetch tool, and cite what you found. Trim the theatrics before they cost clarity.`

// Provider is one backend's settings.
type Provider struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
}

// ProviderDef is a provider named in config: a backend, where to reach it, and
// optionally what it has.
//
// The field names are dross's own — the same provider/base_url/api_key the
// worker, chat, reviewer and compactor blocks already use — rather than
// another agent's spelling of the same idea. A config should read consistently
// with itself before it reads familiarly to somebody arriving from elsewhere.
type ProviderDef struct {
	Name string `json:"name,omitempty"` // display name; defaults to the id
	// Provider is the backend: "openai" (OpenAI-compatible, the default),
	// "anthropic", "gemini", "bedrock", … — the same values the top-level
	// provider field takes.
	Provider string `json:"provider,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
	// APIKeyEnv names an environment variable to read the key from, for a
	// config that is committed somewhere.
	APIKeyEnv string `json:"api_key_env,omitempty"`
	// Models is optional. Left empty for a local or explicitly-pointed
	// provider, dross asks the endpoint — a hand-written list of a local
	// server's models is stale the moment anything is pulled.
	Models []string `json:"models,omitempty"`
}

// File is the on-disk config. All fields optional.
type File struct {
	Name           string            `json:"name,omitempty"`            // app/wordmark name shown in the TUI
	IdentityPrompt string            `json:"identity_prompt,omitempty"` // system-prompt addendum (persona/task framing)
	MaxTurns       int               `json:"max_turns,omitempty"`       // agent tool-loop cap (0 = default 200)
	Welcome        string            `json:"welcome,omitempty"`         // empty-state layout: clean|skills|columns|boxed|centered
	Theme          string            `json:"theme,omitempty"`           // TUI color theme: crystal|ember|matrix|grape|mono
	Colors         map[string]string `json:"colors,omitempty"`          // per-role hex overrides on top of the theme
	Gradient       []string          `json:"gradient,omitempty"`        // holographic gradient stops override
	Provider       string            `json:"provider,omitempty"`
	Model          string            `json:"model,omitempty"`
	BaseURL        string            `json:"base_url,omitempty"`
	APIKey         string            `json:"api_key,omitempty"`
	Style          string            `json:"style,omitempty"`
	MaxTokens      int               `json:"max_tokens,omitempty"`
	MaxParallel    int               `json:"max_parallel,omitempty"` // parallel worker subagents
	Models         []string          `json:"models,omitempty"`       // allowed model ids to offer
	Worker         *Provider         `json:"worker,omitempty"`
	Reviewer       *Provider         `json:"reviewer,omitempty"`
	Review         *bool             `json:"review,omitempty"` // disable the code-review skill (default on when a reviewer is set)
	// Thinking turns a reasoning model's deliberation on or off, for backends
	// that expose the switch (ollama, vLLM, SGLang, llama.cpp). Unset sends
	// nothing and leaves the model as it comes; "thinking": false is the
	// setting that makes a reasoning model usable for mechanical work.
	Thinking *bool `json:"thinking,omitempty"`
	// Memory turns off durable per-project notes: "memory": false stops them
	// being loaded into the prompt and takes the memory tool away. Default on.
	// A pointer for the same reason as Review — absent has to mean "on", and a
	// plain bool cannot tell absent from false.
	Memory    *bool     `json:"memory,omitempty"`
	Compactor *Provider `json:"compactor,omitempty"` // token-efficiency skill
	// Chat is the backend Chat sessions answer on. Unset runs them on the lead
	// backend. Reading search results and writing prose about them is a
	// different job from writing code, so it is worth being able to point it at
	// a different — usually smaller and faster — model.
	//
	// Every field inherits from the top level, so the common case is one line:
	// {"chat": {"model": "gemma2:9b"}} keeps the same provider and endpoint and
	// changes only who answers.
	Chat *Provider `json:"chat,omitempty"`
	// Providers are named model backends, merged over the built-in catalog so
	// that a private endpoint can be referred to by name — in `provider`, in a
	// worker or chat block, or in the TUI's picker. An id that already exists
	// is merged field by field, so pointing a known provider somewhere else
	// does not mean restating the rest of it.
	Providers map[string]ProviderDef `json:"providers,omitempty"`
	// ChatModels is what a chat session's model picker offers, when that should
	// differ from Models. Unset falls back to the chat model alone if a chat
	// backend is configured, and to Models otherwise — because offering the
	// lead's models to a chat running on a different server is offering models
	// that server does not have.
	ChatModels []string `json:"chat_models,omitempty"`
	CompactAt  int      `json:"compact_at,omitempty"` // tool-result byte threshold
	// Sandbox confines a session to its workspace: no reading or writing
	// outside it, and toolchains install inside it. Off by default for a local
	// session, where being unable to open the checkout next door is an
	// obstacle; server-driven sessions set it regardless of this.
	Sandbox bool     `json:"sandbox,omitempty"`
	Phrases []string `json:"phrases,omitempty"` // TUI status phrases

	// tools is an allow-list: when set, only these tool ids are enabled (empty =
	// all). permission is per-tool allow|ask|deny — either a plain string, or,
	// for command tools, a map of command glob → decision (opencode-style).
	// Forges are the git hosts a session's code can come from, keyed by host id
	// ("github"). A session starts by cloning a repository, so without one of
	// these there is nothing for it to start on.
	//
	// The OAuth application is registered by whoever runs this server. dross
	// ships none: an OAuth client identifies an installation to GitHub, and a
	// bundled one would have every dross in the world asking for repository
	// access as the same application.
	Forges map[string]ForgeApp `json:"forges,omitempty"`

	Tools      []string              `json:"tools,omitempty"`
	Permission map[string]Permission `json:"permission,omitempty"`
	Plugins    []PluginTool          `json:"plugins,omitempty"`
	MCP        []MCPServer           `json:"mcp,omitempty"` // MCP servers whose tools the agent gains
}

// ForgeApp is one git host's OAuth application.
//
// The secret is named, not written. This file is a deployment's description of
// itself and lives in version control — in a Kubernetes install it is mounted
// from a ConfigMap, which is readable by anyone who can read the namespace. A
// client secret in it would be a credential committed to a repository, which is
// the thing every one of these integrations exists to avoid.
type ForgeApp struct {
	ClientID string `json:"client_id,omitempty"`
	// ClientSecretEnv names the variable holding the secret. Empty defaults to
	// <HOST>_CLIENT_SECRET — GITHUB_CLIENT_SECRET, GITLAB_CLIENT_SECRET —
	// so the common case needs nothing said here at all.
	ClientSecretEnv string `json:"client_secret_env,omitempty"`
	// BaseURL points at a self-hosted instance; empty is the public host.
	BaseURL string `json:"base_url,omitempty"`
	// Slug is a GitHub App's name in URLs — github.com/apps/<slug>. Without it
	// a user who has authorized the app but not installed it anywhere gets an
	// empty repository list and no way to act on it, since authorizing and
	// installing are two separate steps there.
	Slug string `json:"slug,omitempty"`
}

// MCPServer configures an MCP server to connect to. Kept agent-free here to
// avoid an import cycle; callers convert it to agent.MCPServerSpec.
type MCPServer struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport,omitempty"` // "http" (default) or "stdio"
	URL       string            `json:"url,omitempty"`       // http transport
	Headers   map[string]string `json:"headers,omitempty"`   // http transport (auth)
	Command   string            `json:"command,omitempty"`   // stdio transport
	Args      []string          `json:"args,omitempty"`      // stdio transport
	Env       map[string]string `json:"env,omitempty"`       // stdio transport
}

// Permission is one tool's rule: a blanket decision (JSON string) or a map of
// command glob → decision (JSON object), like opencode.
type Permission struct {
	Blanket string            // set when the JSON value was a plain string
	Rules   map[string]string // set when it was an object (glob → allow|ask|deny)
}

func (p *Permission) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		p.Blanket = s
		return nil
	}
	return json.Unmarshal(b, &p.Rules)
}

// PluginTool is an external tool imported without recompiling: the model calls
// Name with JSON args, which the agent pipes to Command's stdin; stdout is the
// result.
type PluginTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Command     []string       `json:"command"` // argv
	Properties  map[string]any `json:"properties,omitempty"`
	Required    []string       `json:"required,omitempty"`
}

// SearchPaths returns the config file locations checked, in order. An explicit
// path (from --config) wins; then $DROSS_CONFIG; then ./dross.json; then
// the user config dir.
func SearchPaths(explicit string) []string {
	var paths []string
	if explicit != "" {
		paths = append(paths, explicit)
	}
	if env := os.Getenv("DROSS_CONFIG"); env != "" {
		paths = append(paths, env)
	}
	paths = append(paths, "dross.json")
	home, _ := os.UserHomeDir()
	dir, _ := os.UserConfigDir()
	if home != "" {
		paths = append(paths, filepath.Join(home, ".dross", "settings.json"))
	}
	if dir != "" {
		paths = append(paths, filepath.Join(dir, "dross", "config.json"))
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
		if f.IdentityPrompt == "" {
			f.IdentityPrompt = DefaultIdentity
		}
		return &f, p, nil
	}
	return &File{IdentityPrompt: DefaultIdentity}, "", nil
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
	case "azure":
		return os.Getenv("AZURE_OPENAI_ENDPOINT"), os.Getenv("AZURE_OPENAI_API_KEY")
	case "gemini":
		return "", os.Getenv("GEMINI_API_KEY")
	}
	return "", ""
}

// APIKeyEnv is the environment variable a provider takes its key from — the
// inverse of EnvFor's lookup, for callers that have to set the variable rather
// than read it. A session pod is handed its credential this way: as a secret
// reference the container resolves at start, never as an argument, which would
// put it in the pod spec for anyone who can read one.
func APIKeyEnv(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "azure":
		return "AZURE_OPENAI_API_KEY"
	case "gemini":
		return "GEMINI_API_KEY"
	}
	// bedrock takes the AWS chain, and an unknown provider has no convention.
	return ""
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
