# ergon

A pluggable, cost-aware, multi-model coding agent. A frontier model plans and
reasons; a cheaper model — down to a free local GPU model — does the bounded
work; the agent routes each unit of work to the cheapest model that can do it
correctly, and reports what it cost. Drivable from a terminal (Charm TUI), a
phone, or an HTTP API.

Written in Go. MIT licensed. `ergon` is Greek for *work*.

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
go install github.com/dougkirkley/ergon/cmd/ergon@latest
```

Or build from source:

```bash
git clone https://github.com/dougkirkley/ergon
cd ergon
go build -o ergon ./cmd/ergon
go test ./...
```

## Quick start

```bash
ergon              # launches the interactive TUI (bare command = TUI)
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
ergon                         # interactive TUI (default; bare command = TUI)
ergon --resume <id>           # resume a prior TUI session (id printed on exit)
ergon agent  [flags] <prompt> # run the agent once against a workspace
ergon serve  [flags]          # HTTP + SSE server (sessions, device relay, SSO)
ergon rc     --server <url>   # link this machine so a phone can drive it
ergon login  --server <url>   # SSO sign-in for a server
ergon brief  [name]           # print a saved handoff brief, or list sessions
```

## Providers

`ergon` speaks to any provider through a small backend registry. Most are
OpenAI-compatible and share one backend, selected by base URL.

| Kind | How |
|------|-----|
| **Local** (ollama, LM Studio, llama.cpp) | no key; `/model ollama` |
| **OpenAI-compatible hosted** (openai, groq, cerebras, deepseek, xai, mistral, openrouter, together, fireworks, deepinfra, perplexity, nebius, hyperbolic, moonshot) | `<PROVIDER>_API_KEY` in env; `/model <id>` |
| **Anthropic** (native) | `ANTHROPIC_API_KEY`; `/model anthropic` |
| **Claude subscription** (`claude-code`) | drives the local `claude` CLI — no API key; `/model claude-code` |

The `/model` picker shows every catalog provider as a subtitle with its models
underneath, is **searchable** (press `/` inside the picker), and selecting a
model auto-configures its provider. You can also point at any endpoint directly:
`/model <kind> <baseURL> [apiKey]`.

See [`docs/providers-roadmap.md`](docs/providers-roadmap.md) for the plan to
reach full OpenCode/Crush provider breadth.

### claude-code (use your Claude subscription, no API key)

The `claude-code` provider shells out to the installed Claude Code CLI in
headless mode, using whatever auth it already has. In *driven* mode ergon
disables Claude Code's own tools and drives its own loop, so ergon's tools,
delegation, and permissions stay in control. `HOLOCRON_CLAUDE_DANGEROUS=1`
bypasses Claude Code's permission prompts for unattended runs.

## Skills & plugins

`ergon` reads the same on-disk formats as Claude Code, so existing skills and
plugins work unchanged.

- **Skills** are `SKILL.md` files with YAML frontmatter (`name`, `description`,
  `allowed-tools`, `disable-model-invocation`, `user-invocable`). Discovered from
  `.ergon/skills/`, `.claude/skills/` (project), `~/.ergon/skills/`,
  `~/.claude/skills/` (user), and installed plugins. The model loads a skill on
  demand via a `skill` tool (progressive disclosure — only the name +
  description stay in context until it's invoked); you can also run one directly
  as `/<skill-name>`. Flat `commands/*.md` files become `/`-commands too.
- **Plugins** bundle skills and commands under a `.claude-plugin/plugin.json`
  manifest. Install them from a **marketplace** — a git repo with a
  `.claude-plugin/marketplace.json` index.

```bash
ergon plugin marketplace add owner/repo      # or a git URL / local path
ergon plugin install <name>[@marketplace]
ergon plugin list
ergon plugin skills                          # list discovered skills
ergon plugin disable <name> | enable <name> | uninstall <name>
ergon plugin marketplace list | remove <name>
```

The same operations are available in the TUI via `/plugin ...`, and `/skills`
lists what's loaded. Installed plugins live under `~/.ergon/`.

## TUI

- **Slash commands** (autocomplete as you type `/`, tab to complete):
  `/model` (provider+model picker with a Lead/Worker toggle), `/usage`
  (token+cost by model), `/tools` (expand full tool commands + output),
  `/skills` (list skills), `/plugin` (manage plugins + marketplaces),
  `/config` (edit the config in `$EDITOR`), `/theme` (switch color theme),
  `/mouse` (toggle mouse capture), `/clear`, `/init` (generate `AGENTS.md`),
  `/help`, `/login`, `/rc`.
  User-invocable skills also appear as `/<skill-name>`.
- **Mouse** is off by default so your terminal's text selection / copy-paste
  works; `/mouse` turns on click-to-expand tool output (and scroll-wheel
  viewport scrolling) at the cost of native selection.
- The model can also edit `ergon`'s own config on request via a built-in
  `config` tool (e.g. "set my worker to qwen3-coder").
- **`!cmd`** — run a shell command directly (gold prompt border).
- **`ctrl+b`** — while a foreground `bash` command is running, move it to the
  background; the agent loop continues and the command shows up in `/tools` /
  the shells viewer (read it later with the `shell_output` tool).
- **`@path`** — inline a file's contents into your prompt.
- **Permission modes** (⇥ to cycle): `auto`, `manual` (ask before edits +
  commands), `accept edits` (auto edits, ask for commands), `plan` (read-only).
  Enforced in the agent loop.
- **Tool output** — the feed shows a one-line preview; click a tool line, or run
  `/tools`, to expand the full command and output.
- **Conversation memory** across prompts; `/clear` resets it.
- **`←`** opens the session switcher; **`?`** shows shortcuts.

## Configuration

Config is optional JSON, searched in order: `--config <path>`, `$ERGON_CONFIG`,
`./ergon.json`, `~/.ergon/settings.json`, then the user config dir
(`~/.config/ergon/config.json`, or `~/Library/Application Support/ergon/config.json`
on macOS). `/model` writes your provider/model choice here.

```json
{
  "name": "ergon",
  "identity_prompt": "You are a Go specialist. Prefer stdlib. Explain terse.",
  "welcome": "clean",
  "provider": "ollama",
  "base_url": "http://localhost:11434/v1",
  "model": "qwen3-coder:30b",

  "worker": { "provider": "openai", "base_url": "http://localhost:11434/v1", "model": "qwen2.5-coder:7b" },

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

  "phrases": ["Channeling the Force", "Consulting the Jedi Council"]
}
```

- **name** — the wordmark/branding shown in the TUI.
- **identity_prompt** — a system-prompt addendum (persona/task framing) prepended
  to any `AGENTS.md`/`CLAUDE.md` context.
- **welcome** — empty-state layout: `clean` (default), `boxed`, or `centered`
  (the original big centered wordmark).
- **theme** — TUI color theme: `crystal` (default), `ember`, `matrix`, `grape`,
  or `mono`. Switch live with `/theme [name]` (bare `/theme` cycles); override
  once with `ergon --theme <name>`.
- **worker** — a cheaper model the lead can `delegate` bounded subtasks to.
- **tools** — allow-list of enabled tool ids (empty = all). Built-ins: `read`,
  `write`, `edit`, `bash`, `monitor`, `shell_output`, `web_fetch`, `delegate`,
  `publish`, `ask_user`.
- **permission** — per-tool `allow`|`ask`|`deny`; for command tools a map of
  command glob → decision (opencode-style). Enforced regardless of the UI mode.
- **plugins** — external command tools imported without recompiling: the model
  calls the tool, ergon pipes the JSON args to the command's stdin and returns
  its stdout.
- **phrases** — the rotating status lines shown while the agent works.

`AGENTS.md` / `CLAUDE.md` in the workspace are auto-loaded into the system prompt.

## Cost-aware delegation

With a `worker` configured, the lead model gains a `delegate` tool: it hands a
self-contained subtask to the cheaper/local worker, which returns a *conclusion*
(not file dumps), keeping the expensive model's context small. `tasks: [...]`
runs several independent subtasks in parallel.

## serve — server, sessions, device relay

`ergon serve` hosts the embedded web client plus a JSON/SSE API: multiple
sessions, sandboxed per-session workspaces, and a device relay. `ergon rc
--server <url>` links a computer so a phone app can drive sessions that run on
it, authenticated as the SSO user (`ergon login`). Serve supports SSO via any
OIDC provider or Cloudflare Access, plus a static bearer token for programmatic
clients. See `ergon serve --help`.

## Layout

```
cmd/ergon              # CLI entry
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

	"github.com/dougkirkley/ergon/pkg/agent"
	"github.com/dougkirkley/ergon/pkg/provider"
	_ "github.com/dougkirkley/ergon/pkg/provider/anthropic" // registers "anthropic"
	_ "github.com/dougkirkley/ergon/pkg/provider/openai"    // registers "openai"
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

## License

MIT.
