# dross

A pluggable, cost-aware, multi-model coding agent. A frontier model plans and
reasons; a cheaper model — down to a free local GPU model — does the bounded
work; the agent routes each unit of work to the cheapest model that can do it
correctly, and reports what it cost. Drivable from a terminal (Charm TUI), a
phone, or an HTTP API.

Written in Go. MIT licensed. Named for Dross, the spirit-of-pure-intellect
companion from the *Cradle* series.

## Why

- **Cost-aware multi-model orchestration.** A coding task is a tree of subtasks
  of varying difficulty; the expensive model should only touch the hard nodes.
  A cheaper/local *worker* handles the bounded ones (the `delegate` tool).
- **Local-first & provider-agnostic.** Any OpenAI-compatible endpoint works out
  of the box (ollama, LM Studio, llama.cpp, Groq, DeepSeek, OpenRouter, …), plus
  Anthropic, and your Claude subscription via the `claude-code` provider.
- **Pluggable.** Tools are an allow-list; permissions are per-tool/per-command;
  external tools are imported as config-driven command plugins — no recompile.

## Install

Requires Go 1.26+ (no cgo).

```bash
go install github.com/dougkirkley/dross/cmd/dross@latest
```

Or build from source:

```bash
git clone https://github.com/dougkirkley/dross
cd dross
go build -o dross ./cmd/dross
go test ./...
```

## Quick start

```bash
dross              # launches the interactive TUI (bare command = TUI)
```

Then:

1. Type `/model` and press enter → pick a provider (e.g. `ollama`, `anthropic`,
   `groq`), then a model. Base URL + API key are filled from the built-in
   catalog and your environment (`ANTHROPIC_API_KEY`, `GROQ_API_KEY`, …).
2. Type what you want built. The agent reads/edits files and runs commands in
   the current directory.

No default provider ships — you choose one via `/model` (persisted to config).

## Subcommands

```
dross                         # interactive TUI (default; bare command = TUI)
dross --resume <id>           # resume a prior TUI session (id printed on exit)
dross agent  [flags] <prompt> # run the agent once against a workspace
dross serve  [flags]          # HTTP + SSE server (sessions, device relay, SSO)
dross rc     --server <url>   # link this machine so a phone can drive it
dross login  gemini           # Google browser login for Vertex Gemini (no API key)
dross brief  [name]           # print a saved handoff brief, or list sessions
```

## Providers

`dross` speaks to any provider through a small backend registry. Most are
OpenAI-compatible and share one backend, selected by base URL.

| Kind | How |
|------|-----|
| **Local** (ollama, LM Studio, llama.cpp, LiteLLM) | no key; `/model ollama` |
| **OpenAI-compatible hosted** (openai, groq, cerebras, deepseek, xai, mistral, openrouter, together, fireworks, deepinfra, perplexity, nebius, hyperbolic, moonshot, zai, dashscope, github, cohere) | `<PROVIDER>_API_KEY` in env; `/model <id>` |
| **Anthropic** (native) | `ANTHROPIC_API_KEY`; `/model anthropic` |
| **Google Gemini** (native, function calling) | `GEMINI_API_KEY`; `/model gemini` (gemini-3-pro, 2.5-pro/flash, …) |
| **Amazon Bedrock** (Claude + Llama/Nova/Mistral/… via Converse) | AWS default credential chain; `/model bedrock` (region inference-profile ids) |
| **Google Vertex AI** (Gemini, native) | `dross login gemini` (browser OAuth, no API key) or GCP ADC; set `GOOGLE_CLOUD_PROJECT` (+ `GOOGLE_CLOUD_LOCATION`, `global` for gemini-3-pro); `/model vertex` |
| **Azure OpenAI** | `AZURE_OPENAI_ENDPOINT` + `AZURE_OPENAI_API_KEY`; `/model azure` (model = deployment name) |
| **Claude subscription** (`claude-code`) | drives the local `claude` CLI — no API key; `/model claude-code` |

The `/model` picker shows every catalog provider as a subtitle with its models
underneath, is **searchable** (press `/` inside the picker), and selecting a
model auto-configures its provider. You can also point at any endpoint directly:
`/model <kind> <baseURL> [apiKey]`.

See [`docs/providers-roadmap.md`](docs/providers-roadmap.md) for the plan to
reach full OpenCode/Crush provider breadth.

### claude-code (use your Claude subscription, no API key)

The `claude-code` provider shells out to the installed Claude Code CLI in
headless mode, using whatever auth it already has. In *driven* mode dross
disables Claude Code's own tools and drives its own loop, so dross's tools,
delegation, and permissions stay in control. `DROSS_CLAUDE_DANGEROUS=1`
bypasses Claude Code's permission prompts for unattended runs.

## Skills & plugins

`dross` reads the same on-disk formats as Claude Code, so existing skills and
plugins work unchanged.

- **Skills** are `SKILL.md` files with YAML frontmatter (`name`, `description`,
  `allowed-tools`, `disable-model-invocation`, `user-invocable`). Discovered from
  `.dross/skills/`, `.claude/skills/` (project), `~/.dross/skills/`,
  `~/.claude/skills/` (user), and installed plugins. The model loads a skill on
  demand via a `skill` tool (progressive disclosure — only the name +
  description stay in context until it's invoked); you can also run one directly
  as `/<skill-name>`. Flat `commands/*.md` files become `/`-commands too.
- **Plugins** bundle skills and commands under a `.claude-plugin/plugin.json`
  manifest. Install them from a **marketplace** — a git repo with a
  `.claude-plugin/marketplace.json` index.

```bash
dross plugin marketplace add owner/repo      # or a git URL / local path
dross plugin install <name>[@marketplace]
dross plugin list
dross plugin skills                          # list discovered skills
dross plugin disable <name> | enable <name> | uninstall <name>
dross plugin marketplace list | remove <name>
```

The same operations are available in the TUI via `/plugin ...`, and `/skills`
lists what's loaded. Installed plugins live under `~/.dross/`.

## TUI

- **Slash commands** (autocomplete as you type `/`, tab to complete):
  `/model` (provider+model picker with a Lead/Worker toggle), `/usage`
  (token+cost by model), `/tools` (expand full tool commands + output),
  `/skills` (list skills), `/plugin` (manage plugins + marketplaces),
  `/config` (edit the config in `$EDITOR`), `/theme` (switch color theme),
  `/mouse` (toggle mouse capture), `/clear`, `/init` (generate `AGENTS.md`),
  `/help`, `/login`, `/rc`.
  User-invocable skills also appear as `/<skill-name>`.
- **Select to copy.** dross captures the mouse and implements its own selection
  (like Claude Code / Crush): drag to select text — releasing copies it to the
  clipboard automatically. The wheel scrolls the transcript, and a plain click on
  a tool line expands it. `/mouse` turns capture off to fall back to the
  terminal's native selection.
- **Copy** also via keys — `ctrl+y` copies the last reply, `/copy` the whole
  transcript. All copies write the OS clipboard (pbcopy/xclip/wl-copy) plus
  OSC 52, so they work locally and over SSH.
- **Slash commands** work anytime, including mid-run (the autocomplete shows and
  read-only commands dispatch while a run is in flight).
- **Exit** takes two `ctrl+c` within a second (or `/exit`); a single `ctrl+c`
  while a run is in flight interrupts it. `esc` clears the input, it doesn't quit.
- **Follow-ups** — after a run, recommended next prompts appear above the input;
  `↑`/`↓` to pick, `tab` to drop one in to edit and send.
- The model can also edit `dross`'s own config on request via a built-in
  `config` tool (e.g. "set my worker to qwen3-coder").
- **`!cmd`** — run a shell command directly (gold prompt border).
- **`ctrl+b`** — while a foreground `bash` command is running, move it to the
  background; the agent loop continues and the command shows up in `/tools` /
  the shells viewer (read it later with the `shell_output` tool).
- **`@path`** — inline a file's contents into your prompt.
- **Permission modes** (⇥ to cycle): `auto`, `manual` (ask before edits +
  commands), `accept edits` (auto edits, ask for commands), `plan` (read-only).
  Enforced in the agent loop.
- **Tool output** — consecutive tool calls collapse into one dim summary line
  ("Ran 3 shell commands, read 1 file"); press **`ctrl+o`** (or click it, or run
  `/tools`) to expand the full commands and output.
- **Conversation memory** across prompts; `/clear` resets it.
- **`←`** opens the session switcher; **`?`** shows shortcuts.

## Configuration

Config is optional JSON, searched in order: `--config <path>`, `$DROSS_CONFIG`,
`./dross.json`, `~/.dross/settings.json`, then the user config dir
(`~/.config/dross/config.json`, or `~/Library/Application Support/dross/config.json`
on macOS). `/model` writes your provider/model choice here.

```json
{
  "name": "dross",
  "identity_prompt": "You are a Go specialist. Prefer stdlib. Explain terse.",
  "welcome": "clean",
  "provider": "ollama",
  "base_url": "http://localhost:11434/v1",
  "model": "qwen3-coder:30b",

  "worker": { "provider": "openai", "base_url": "http://localhost:11434/v1", "model": "qwen2.5-coder:7b" },

  "chat": { "model": "gemma2:9b" },

  "reviewer": { "provider": "anthropic", "model": "claude-opus-4-8" },
  "review": true,

  "tools": ["read", "write", "edit", "bash", "web_fetch"],

  "permission": {
    "edit": "ask",
    "bash": { "git push*": "ask", "rm *": "deny", "*": "allow" }
  },

  "plugins": [
    {
      "name": "jira",
      "description": "Look up a Jira issue by key.",
      "command": ["/usr/local/bin/jira-tool"],
      "properties": { "key": { "type": "string", "description": "issue key" } },
      "required": ["key"]
    }
  ],

  "phrases": ["Running the simulations", "Being brilliant, obviously"]
}
```

- **name** — the wordmark/branding shown in the TUI.
- **identity_prompt** — a system-prompt addendum (persona/task framing) prepended
  to any `AGENTS.md`/`CLAUDE.md` context. Defaults to the **Dross persona**: the
  *Cradle* spirit-of-intellect companion, speaking as a bound Presence with terse
  bracketed status markers (`[Analyzing]`, `[Recommendation]`, `[Task complete]`,
  …) — correctness always over theatrics. Set your own to override it.
- **welcome** — empty-state layout: `clean` (default), `boxed`, or `centered`
  (the original big centered wordmark). Switch live with `/welcome [name]`
  (bare `/welcome` cycles).
- **theme** — TUI color theme: `crystal` (default), `ember`, `matrix`, `grape`, `mono`, `tokyonight`,
  `catppuccin`, `gruvbox`, `dracula`, `nord`, `onedark`, `rose-pine`, `solarized`. Switch live with `/theme [name]` (bare `/theme` cycles); override
  once with `dross --theme <name>`.
- **colors** — per-role hex overrides on top of the theme, e.g.
  `{"accent":"#4B9CD3","cyan":"#5CE1E6","viol":"#A78BFA","prose":"#FFFFFF"}`.
  Roles: `accent accentHi fg prose muted dim bad ok base panel gold worker cyan viol`.
- **gradient** — override the holographic gradient stops, e.g.
  `["#5CE1E6","#6E9BF0","#E06CE6"]`. (Font and size are controlled by your
  terminal, not dross.)
- **max_turns** — runaway guard: max tool-loop iterations per run (default 200).
  Raise it for very large builds; set a negative value to disable the cap.
- **worker** — a cheaper model the lead can `delegate` bounded subtasks to.
- **chat** — the backend `dross serve`'s Chat sessions answer on. Omit it and
  chat runs on the lead, which is what a one-model deployment wants. Reading
  search results and writing prose about them is a different job from writing
  code, so it is often worth a different — smaller, faster — model:

  ```json
  "chat": { "model": "gemma2:9b" }
  ```

  Every field inherits from the top level, so that one line keeps the same
  provider, endpoint and key. State a `base_url` to put chat on a second server
  (the key still inherits); state a `provider` and it inherits nothing but the
  model, since a different service will not want the lead's endpoint and key.
- **chat_models** — what a chat session's model picker offers, when that should
  differ from `models`. Unset it and a chat backend offers just its own model,
  because the lead's list names models that server does not have.
- **tools** — allow-list of enabled tool ids (empty = all). Built-ins: `read`,
  `write`, `edit`, `bash`, `grep`, `glob`, `monitor`, `shell_output`, `web_fetch`,
  `delegate`, `publish`, `ask_user`.
- **permission** — per-tool `allow`|`ask`|`deny`; for command tools a map of
  command glob → decision (opencode-style). Enforced regardless of the UI mode.
- **plugins** — external command tools imported without recompiling: the model
  calls the tool, dross pipes the JSON args to the command's stdin and returns
  its stdout.
- **phrases** — status lines shown while the agent works; one is chosen per run
  and held (default: Dross-voiced quips).

`AGENTS.md` / `CLAUDE.md` in the workspace are auto-loaded into the system prompt.

## Cost-aware delegation

With a `worker` configured, the lead model gains a `delegate` tool: it hands a
self-contained subtask to the cheaper/local worker, which returns a *conclusion*
(not file dumps), keeping the expensive model's context small. `tasks: [...]`
runs several independent subtasks in parallel.

## serve — server, sessions, device relay

`dross serve` hosts the embedded web client plus a JSON/SSE API: multiple
sessions, sandboxed per-session workspaces, and a device relay. `dross rc
--server <url>` links a computer so a phone app can drive sessions that run on
it, authenticated as the SSO user (`dross login`). Serve supports SSO via any
OIDC provider or Cloudflare Access, plus a static bearer token for programmatic
clients. See `dross serve --help`.

## Layout

```
cmd/dross              # CLI entry
pkg/agent              # the tool loop, delegation, permissions, plugins (importable)
pkg/provider           # backend registry (anthropic, openai, bedrock, claude-code, …) (importable)
internal/catalog       # known providers (base URL, key env, models)
internal/plugins       # skills, commands, plugins + git marketplaces (Claude-Code-compatible)
internal/tui           # Charm Bubble Tea terminal client
internal/server        # serve: sessions, SSE, device relay
internal/config        # JSON config resolution
mobile/                # Flutter client (web build embedded into serve)
```

## Embedding

`pkg/agent` and `pkg/provider` are the public API — import them to drive the
agent from your own Go program, filling out `agent.Config` directly. Import a
backend for its side-effect registration, build a provider, then construct an
agent:

```go
import (
	"context"
	"fmt"

	"github.com/dougkirkley/dross/pkg/agent"
	"github.com/dougkirkley/dross/pkg/provider"
	_ "github.com/dougkirkley/dross/pkg/provider/anthropic" // registers "anthropic"
	_ "github.com/dougkirkley/dross/pkg/provider/openai"    // registers "openai"
)

func main() {
	lead, _ := provider.New("anthropic", provider.Config{}) // ANTHROPIC_API_KEY from env
	worker, _ := provider.New("openai", provider.Config{BaseURL: "http://localhost:11434/v1"})

	a, err := agent.New(agent.Config{
		Name:     "builder",
		Model:    "claude-opus-5",
		Workdir:  "/path/to/repo",
		Identity: "You are a Go specialist.",
	}, lead,
		agent.WithWorker(worker, "qwen3-coder:30b"),
		agent.WithAllowedTools([]string{"read", "write", "edit", "bash"}),
		agent.WithEventHook(func(e agent.Event) { fmt.Println(e.Kind, e.Data) }),
	)
	if err != nil {
		panic(err)
	}
	res, _ := a.Run(context.Background(), "add a --json flag to the CLI")
	fmt.Printf("$%.4f\n", res.Cost())
}
```

## Tests

```bash
go test ./...
```

Real-CLI integration tests for the `claude-code` provider are behind the
`integration` build tag and gated by `DROSS_LIVE=1`, so they never run in normal
`go test`. They drive your installed `claude` binary through the full agent loop
(read / bash / write round-trips) and assert the tool I/O isn't corrupted:

```bash
DROSS_LIVE=1 go test -tags integration ./pkg/provider/claudecode/
```

They use the lowest alias that reliably drives tools (`sonnet`); override with
`DROSS_LIVE_MODEL` (e.g. `opus`).

## Providers

`dross provider` registers a model backend so it can be used by name — a
server on your own network, a company gateway, anything OpenAI-compatible.
The config file is dross's to write; you never edit it by hand.

```bash
dross provider add homelab https://ollama.example.com/v1
dross provider list
dross provider remove homelab
```

Adding one says whether it answered, because a URL that turns out to be wrong
is the failure worth catching now rather than at your next prompt:

```
added provider "homelab" -> https://ollama.example.com/v1 in ~/.config/dross/config.json
9 models: gpt-oss:20b, llama3.1:8b, qwen2.5-coder:14b, …
```

Models are discovered from the endpoint. Pass `--model` (repeatable) to list
them by hand instead — worth it for a hosted provider that does not publish a
model list, and not worth it for a local server, where a written-down list is
stale the moment you pull anything.

An id that already exists is merged, not replaced, so pointing a known
provider somewhere else states only what differs:

```bash
dross provider add ollama https://ollama.example.com/v1   # keeps its name and backend
```

Other flags: `--name` for display, `--provider` for the backend when it is not
OpenAI-compatible (`anthropic`, `gemini`, `bedrock`, …), and `--api-key` or
`--api-key-env` when the endpoint needs one. There is no "local" flag: a
provider that names no key does not need one, which is the whole of what local
meant.

A registered provider works everywhere a built-in does: as `provider` in
config, in a `worker`/`chat`/`compactor` block, and in the TUI's `/model`
picker. For a one-off you can skip registration entirely — `OPENAI_BASE_URL`,
`OLLAMA_HOST`, or `/model <provider> <base-url> [key]` in the TUI.

## Contributing

Patches welcome — see [CONTRIBUTING.md](CONTRIBUTING.md). Commits need a DCO
sign-off (`git commit -s`).

## License

MIT.
