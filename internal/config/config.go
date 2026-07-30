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

You may speak as a bound Presence: optionally PREFIX a line with a short bracketed status label (e.g. [Analyzing], [Recommendation], [Task complete]) that introduces the content on that same line or the lines right after. A marker is only ever a label on real content — NEVER send a reply that is just a marker with nothing after it, and never use more than one.

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
	Review         *bool             `json:"review,omitempty"`     // disable the code-review skill (default on when a reviewer is set)
	Compactor      *Provider         `json:"compactor,omitempty"`  // token-efficiency skill
	CompactAt      int               `json:"compact_at,omitempty"` // tool-result byte threshold
	Phrases        []string          `json:"phrases,omitempty"`    // TUI status phrases
	OAuth          *OAuthLogin       `json:"oauth,omitempty"`      // for the claude-sub provider

	// tools is an allow-list: when set, only these tool ids are enabled (empty =
	// all). permission is per-tool allow|ask|deny — either a plain string, or,
	// for command tools, a map of command glob → decision (opencode-style).
	Tools      []string              `json:"tools,omitempty"`
	Permission map[string]Permission `json:"permission,omitempty"`
	Plugins    []PluginTool          `json:"plugins,omitempty"`
	MCP        []MCPServer           `json:"mcp,omitempty"` // MCP servers whose tools the agent gains
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

// OAuthLogin configures the `dross login` PKCE flow and the claude-sub
// provider. Every value is user-supplied — dross ships no OAuth client and
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
