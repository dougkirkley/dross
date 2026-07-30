# dross — design

Authoritative design. (Formerly *holocron*; renamed to **dross** for an
open-source release, including the deployment hostname
`dross.farmerjohnparmesan.com`.)

Supersedes `CRUSH_INTEGRATION.md` and `OPENCODE_DEPLOYMENT.md`, both of which are
AI-generated and factually wrong (Crush does not "generate UI components
programmatically"; `opencode/server:latest` is not a real image). Delete those
two once this is reviewed.

## What dross is

A self-hosted coding agent whose one differentiator is **cost-aware
multi-model orchestration**: an expensive model plans and reasons, cheaper
models — down to a free local GPU model — do the bounded work, and the agent
routes each unit of work to the cheapest model that can do it correctly. It has
the planning and subagent workflow feel of Claude Code, drivable from a phone
and from a Charm-styled terminal, with token cost as the primary optimization
target.

The thesis is not invented. This homelab already does it by hand: a global
instruction to delegate narrow subtasks to the local model, an `ollama-delegate`
MCP, and a tuned qwen3-coder:30b on the node2 GPU. dross makes the router the
product.

## As-built layout

```
cmd/dross              # CLI entry (bare `dross` = TUI; agent/serve/rc/login/... subcommands)
pkg/agent              # the tool loop, delegation, permissions, plugins, conversation memory (importable)
pkg/provider           # backend registry (anthropic, openai-compatible, bedrock, claude-sub, claude-code) (importable)
internal/catalog       # known providers (id, base URL, key env, preview models)
internal/tui           # Charm Bubble Tea terminal client (holographic-crystal theme)
internal/server        # serve: sessions, SSE, device relay, SSO
internal/relay         # device relay + SSO loopback login (shared by cli + tui)
internal/config        # JSON config resolution (dross.json, back-compat dross paths)
mobile/                # Flutter client (web build embedded into serve)
```

`pkg/agent` and `pkg/provider` are the public embedding surface: another Go
program imports them, fills out `agent.Config` directly, builds a
`provider.Provider`, and calls `agent.New(...).Run(...)`. Everything a consumer
must name lives under `pkg/`; `internal/{config,catalog,subauth}` are
implementation detail the backends pull in transitively (allowed within the
module) but consumers never reference. See the Embedding section in the README.

The original skeleton (`internal/ai/provider.go` synchronous `SendMessage`,
stub `claude`/`gemini`/`codex`/`antigravity` providers, Vue 3 frontend) has been
replaced: the `Provider` interface is now streaming with tool calls and token
accounting, dross owns the agent loop itself, and the client is Flutter (with a
Bubble Tea TUI as the primary local client), not Vue.

## The cost stack (the whole point)

Token efficiency here is not a single trick; it is a stack, cheapest first. All
of the Anthropic pieces are first-party features, verified against the current
Go SDK and API.

**1. Model tiers — route by task difficulty.**

| Tier | Model | $/1M in·out | Role |
|---|---|---|---|
| Local | `qwen3-coder:30b` (ollama, node2 GPU) | **$0** | mechanical edits, search, file summaries, test stubs |
| Cheap | `claude-haiku-4-5` | $1 · $5 | classification, routing decisions, simple tools |
| Mid | `claude-sonnet-5` | $3 · $15 | most coding, near-Opus quality |
| Lead | `claude-opus-5` | $5 · $25 | planning, hard debugging, cross-file reasoning |

Every token the local model handles is free after the hardware — which we
already paid for and just tuned to ~36 tok/s. The router's job is to keep as
much work as possible in the $0 tier and escalate only what needs it.

**2. Prompt caching** (Anthropic). Cache reads cost ~0.1x, writes ~1.25x. Opus 5
caches prefixes as small as 512 tokens. The large system prompt and stable
project context get cached once; every subsequent turn pays a tenth. This is the
single biggest lever on a long agent session. Keep the system prompt frozen
(no interpolated timestamps), tools deterministically ordered, volatile content
last.

**3. Effort levels** (`output_config.effort`: low/medium/high/xhigh/max). Cheap
subtasks run at `low`; hard planning at `xhigh`. Effort is the per-call
intelligence/cost dial on the paid tiers.

**4. Subagents that return conclusions, not context.** Claude Code's core
token-saving move: a subagent reads many files and returns only the answer, so
the expensive lead context never ingests the file dumps. Run those subagents on
the **local** or cheap tier. This is where the planning/subagent feel and the
cost goal are the same feature.

**5. Adaptive thinking** (on by default on Opus 5). The model self-regulates
reasoning depth per task — no fixed thinking budget to tune.

**6. Context editing / compaction** (Anthropic beta). Prune stale tool results
and compact old turns so fewer tokens stay resident every turn.

**7. Task budgets** (Anthropic beta). Hand the agent a cumulative token ceiling
for a whole agentic loop so it paces itself instead of being cut off.

**Cost accounting is a headline output, not telemetry.** Every run reports
tokens and $ per tier and the local/paid split. "This run cost $0.04 because 80%
went to the GPU" is the product's proof. The TUI surfaces this live via `/usage`
(per-model context bars) and a bottom-right cost segment.

## Context economy (first-class)

Cost is dominated by tokens per turn, and tokens per turn are dominated by how
much context rides along. Keeping context small is therefore a first-class
concern, not an afterthought — a set of deliberate mechanisms, each a lever the
user can reach for:

1. **Delegation returns conclusions, not dumps** (`delegate` tool, built). A
   worker reads many files and returns a summary; the expensive lead context
   never ingests the file bodies. Workers run on the cheap/local tier.
2. **Handoff brief + resume** (`brief` subcommand, built). A session ends by
   writing a compact brief — task, state, files, next steps, gotchas — and a new
   session resumes from that brief, not the full transcript. This is the cheap
   alternative to transcript compaction: keep each session short and hand off
   only what matters. Verified live: a fresh-workspace session continued
   correctly from a brief alone.
3. **Output style / verbosity** (`-style`, built). `normal` | `terse` |
   `caveman` injects an output-compression directive; terser output means fewer
   *output* tokens (the expensive kind) while code, commands, and errors stay
   verbatim. A knob on the agent, not a global.
4. **Token-efficiency compactor** (`compactor` config, built). A cheap model
   compacts oversized tool results past a byte threshold (`compact_at`) before
   they re-bill on every subsequent turn.

Roadmap (same pillar):

5. **Auto-compact via rolling handoff.** When a single run's turns/tokens cross
   a threshold, generate a brief mid-run, discard the session, and restart from
   the brief — provider-agnostic rolling compaction reusing the brief machinery.
6. **Native Anthropic context editing** for the anthropic provider
   (`clear_tool_uses`, compaction beta) where available — a provider-specific
   accelerant under the same interface.

The through-line: every token not sent is a token not paid for, and on the local
tier the cheapest token is the one handled by the GPU. Context economy and the
cost stack are the same objective from two angles.

## The router (the intelligence)

Everything above is plumbing that exists in every agent; the router is what
makes dross dross. Start with an explicit, inspectable policy — not a
learned model — so every routing decision is debuggable.

1. **Lead decomposes.** Given a task, the lead (Opus 5, `xhigh`) emits a plan of
   subtasks, each tagged with difficulty and whether it is *self-contained*
   (all context fits in the subtask prompt — the hard constraint, since a worker
   can't see the session).
2. **Router assigns.** Self-contained + bounded → local worker. Needs judgment or
   broad context → keep on lead/mid. The routing decision itself is cheap enough
   to run on Haiku.
3. **Worker executes** in isolation with a fully self-contained prompt (the exact
   discipline `delegate_to_local_model` already enforces).
4. **Verify.** Worker output is a draft, never trusted — qwen3-coder is
   confidently wrong at times (documented homelab behavior). Cheap structural
   checks always (compiles / parses / matches signature); lead review only when
   the subtask is load-bearing.
5. **Escalate on failure.** Worker fails verification twice → lead takes it over.
   Bounded retries, then escalate, then surface.

A fast-path skips decomposition for trivially small tasks (one model, no plan) so
routing overhead never exceeds what it saves.

## Provider interface

```go
type Provider interface {
    // Stream a completion; emits text/thinking deltas and tool calls.
    Stream(ctx context.Context, req Request) (<-chan Event, error)
    // Model metadata: context window, pricing, tool support, effort levels.
    Models() []ModelInfo
}
```

Backends registered in `internal/provider`:

- `anthropic` — official Go SDK (`github.com/anthropics/anthropic-sdk-go`);
  streaming, cache-control on system blocks, effort.
- `openai` — any OpenAI-compatible `/v1` endpoint (ollama, LM Studio, llama.cpp,
  Groq, DeepSeek, OpenRouter, …), selected by base URL. Includes an XML
  tool-call fallback for models (e.g. qwen3-coder) that emit tool calls as text.
- `bedrock` — Anthropic on AWS Bedrock.
- `claude-sub` — a Claude subscription via user-supplied OAuth (`dross login`).
- `claude-code` — shells out to the installed `claude` CLI in *driven* mode:
  dross disables Claude Code's own tools and drives its own loop, so dross's
  tools/delegation/permissions stay in control. No API key.

`internal/catalog` lists ~17 known providers (id, base URL, key env, preview
models) so the `/model` picker can group models under providers and
auto-configure the chosen provider from the catalog + environment. See
[`docs/providers-roadmap.md`](docs/providers-roadmap.md) for the plan to reach
full OpenCode/Crush breadth.

## Tools

Tool quality shapes agent behavior more than model choice within a tier.
Built-in set (`internal/agent`), correctness over breadth:

- `read` (line-numbered, range, refuses huge files)
- `edit` (exact-match replacement, unique-match required, legible failure — this
  one tool disproportionately determines how good the agent feels)
- `write`, `bash` (permission-gated), plus `shell_output` for background shells
- `monitor` (poll a command until it exits 0)
- `web_fetch` (fetch a URL, strip to text)
- `delegate` (cost-aware worker), `publish`, `ask_user` (conditional)

Tools are an **allow-list** (`tools` config; empty = all). **Permissions** are
per-tool `allow`/`ask`/`deny`, or for command tools a map of command glob →
decision (opencode-style), enforced in the loop regardless of the UI mode.
**Plugins** import external command tools without recompiling: the model calls
the tool, dross pipes the JSON args to the command's stdin and returns its
stdout.

## Permission modes (Claude Code parity)

Four modes as presets over the per-tool ruleset, cycled with ⇥ in the TUI and
enforced in the agent loop:

| Mode | Policy |
|---|---|
| Manual | ask before edits + commands |
| Auto | every tool allow |
| Accept edits | auto edits, ask for commands |
| Plan | lead runs read-only, produces a plan, no mutations |

Live `ask` prompts flow over SSE to the phone and back — that is the
push-notification path.

## Clients — the Crush feel without the fork

Crush's look comes from Charm's **public, MIT-licensed** libraries; only Crush's
agent logic is walled in `internal/`. So dross gets the entire Crush aesthetic
with zero fork by building a TUI on those libraries:

- `github.com/charmbracelet/bubbletea` — TUI framework (Elm architecture)
- `github.com/charmbracelet/bubbles` — components (viewport, textinput, spinner, progress)
- `github.com/charmbracelet/lipgloss` — styling/layout

Three clients, one core, all over the same HTTP+SSE API:

1. **Bubble Tea TUI** (`internal/tui`, launched by bare `dross`) — the local
   client, with a holographic-crystal theme, grouped/searchable `/model` picker,
   `/usage`, `/tools`, `@file` mentions, `!cmd` shell escape, and slash-command
   autocomplete.
2. **Flutter app** — web build embedded into `serve`; also packaged for
   Android/iOS. Speaks the SSE + session API.
3. Both are peers over the same server API; the server stays client-agnostic.

## Remote control via `dross serve` + device relay

The phone app must drive sessions the way Claude Code's Remote Control does: fire
a task, put the phone away, the run keeps going, reattach later to watch, steer,
or approve. "Remote control" is a property of *where the session lives*, not of
an outbound socket. A long-running server that owns the sessions delivers it
directly, with clients connecting inbound — the `opencode serve` / `crush serve`
model. dross's server entrypoint is the **`serve` subcommand**, not a
Claude-Code-style outbound dial-out.

```
dross serve --addr :8080        # holds sessions, serves HTTP+SSE + embedded web client
```

On top of that, a **device relay** (`internal/relay`, `dross rc` /
`/rc` in the TUI) links a computer to the server as the logged-in SSO user, so a
phone can drive sessions that actually run on that machine. `dross login`
performs an SSO loopback sign-in; the device token is stored in the server's
auth secret. Verified locally: phone-issued prompt ran `uname -s` on the Mac.

What makes it "remote control" is the session model:

- **Sessions are server-side and client-independent.** A run continues to
  completion whether or not any client is connected. `POST
  /sessions/{id}/prompt` is async; closing the phone never pauses the agent.
- **Any client attaches to any session by ID.** Reconnecting fetches full history
  then tails live via SSE, deduping by event id. TUI, web, and phone are peers.
- **Steering works detached.** Interrupt, queued follow-ups, permission replies,
  and mode switches are events processed in order, independent of which client
  (if any) is watching.
- **Lossless reconnect.** Every (re)connect overlaps a history fetch with the
  live stream and dedupes — a permission ask that fired while the phone was
  backgrounded is still waiting on reattach, not lost.

Exposure: `dross serve` stays behind Cloudflare Access + a bearer on the one
public hostname; inbound only.

iOS specifics: background URLSession streams get suspended, so a long run won't
stream while the app is backgrounded — use `prompt` async + reconcile on
foreground, and let the **push notification** (permission ask / idle / error) be
the signal to reattach.

## Server API

- `POST /sessions`, `GET /sessions`, `GET /sessions/{id}`
- `POST /sessions/{id}/prompt` (async; returns immediately)
- `GET /sessions/{id}/events`, `GET /events` (SSE)
- `POST /sessions/{id}/permission/{id}/reply`
- device relay + SSO endpoints (`internal/relay` protocol)
- `POST /artifacts`, `GET /a/{id}` — shareable capability links
- `POST /upload`
- `PUT/DELETE /devices/{token}` — APNs registration
- `GET /cost/{sessionID}` — the tier/cost breakdown

Auth: SSO via any OIDC provider or Cloudflare Access, plus a static bearer for
programmatic clients.

## Deployment

Go binary image (`golang:1.26.5-alpine` base; `apk add bash git ca-certificates`
so sandboxed sessions can run commands), built `--platform linux/amd64`, pushed
to `registry.farmerjohnparmesan.com` (image `dross-web`), base + overlay under
`kubernetes/` in the `k8s-homelab` repo. Flux GitOps reconciles it. Needs
in-cluster reachability to ollama (a Service; `ollama.farmerjohnparmesan.com`),
egress to 443 for Anthropic, and a PVC for session/artifact/device state. APNs
`.p8` and other secrets are SOPS/age in-repo, decrypted by flux. Cloudflare
Access on the one public hostname.

Build note: always bump the image tag per deploy and build for `linux/amd64`
(a plain build produces an arm64 image that crash-loops; a same-tag re-push
won't be re-pulled).

## Status

Built: real Anthropic + OpenAI-compatible + bedrock + claude-sub + claude-code
providers; the agent loop with `read`/`edit`/`write`/`bash`/`monitor`/
`shell_output`/`web_fetch`/`delegate`; tool allow-list, per-tool/per-command
permissions, plugins; conversation memory; handoff brief + resume; output styles;
token-efficiency compactor; the Bubble Tea TUI (theme, `/model` catalog picker,
`/usage`, `/tools`, `@file`, `!cmd`, autocomplete, permission gating); `serve`
with sessions + SSE + embedded Flutter web client; device relay + SSO login;
Flutter mobile app (Android/iOS).

Roadmap: the automatic router + policy engine (currently manual lead/worker
selection); auto-compact via rolling handoff; native Anthropic context editing;
provider-catalog breadth (see `docs/providers-roadmap.md`); native Gemini/Vertex.

## Risks

- **Tool-layer quality** is a moat you cross even with a perfect router; a flaky
  `edit` makes the agent feel bad. Keep the built-in tools small and correct.
- **Worker reliability** — the verify/escalate loop is load-bearing; weak
  verification lets bad local output poison runs.
- **Routing overhead** — decomposing every task costs lead tokens; the fast-path
  is required, not optional.
- **Self-contained prompts** — getting the lead to emit genuinely self-contained
  worker prompts is itself a prompt-engineering problem.
- **Solo maintenance** — a real ongoing project vs. bumping an image tag. The
  router gate is the off-ramp: if it doesn't cut cost while holding quality,
  the thesis fails.
