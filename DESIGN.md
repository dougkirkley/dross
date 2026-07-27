# Holocron — design

Authoritative design. Supersedes `CRUSH_INTEGRATION.md` and
`OPENCODE_DEPLOYMENT.md`, both of which are AI-generated and factually wrong
(Crush does not "generate UI components programmatically"; `opencode/server:latest`
is not a real image). Delete those two once this is reviewed.

## What holocron is

A self-hosted coding agent whose one differentiator is **cost-aware
multi-model orchestration**: an expensive model plans and reasons, cheaper
models — down to a free local GPU model — do the bounded work, and the agent
routes each unit of work to the cheapest model that can do it correctly. It has
the planning and subagent workflow feel of Claude Code, drivable from a phone
and from a Charm-styled terminal, with token cost as the primary optimization
target.

The thesis is not invented. This homelab already does it by hand: a global
instruction to delegate narrow subtasks to the local model, an `ollama-delegate`
MCP, and a tuned qwen3-coder:30b on the node2 GPU. Holocron makes the router the
product.

## Where it starts

The repo already has the right skeleton, just not the intelligence:

- `internal/ai/provider.go` — a `Provider` interface (`Init`/`Name`/`SendMessage`)
- `internal/ai/manager.go` — a `Manager` that routes by provider name
- `internal/ai/{claude,gemini,codex,antigravity}.go` — **stubs** (fake string returns)
- `internal/server/{server,handler,sse}.go` — HTTP + SSE broker
- `internal/storage/session.go`, `internal/assets/` — session state, uploads
- `frontend/` — Vue 3 + TS + Vite + Pinia + Tailwind

Two things must change structurally:

1. **The `Provider` interface is synchronous request/response** (`SendMessage → string`).
   A real coding agent needs streaming, tool calls, and token accounting. This
   interface is replaced (below).
2. **Providers are framed as external agent CLIs.** The differentiator wants
   holocron to *be* the agent — own the loop, the tools, and the routing — with
   providers being *models*, not other agents.

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
went to the GPU" is the product's proof.

## Context economy (first-class)

Cost is dominated by tokens per turn, and tokens per turn are dominated by how
much context rides along. Keeping context small is therefore a first-class
concern, not an afterthought — a set of deliberate mechanisms, each a lever the
user can reach for:

1. **Delegation returns conclusions, not dumps** (`delegate` tool, built). A
   worker reads many files and returns a summary; the expensive lead context
   never ingests the file bodies. Workers run on the cheap/local tier.
2. **Handoff brief + resume** (`-session`, built). A session ends by writing a
   compact brief — task, state, files, next steps, gotchas — and a new session
   resumes from that brief, not the full transcript. This is the cheap
   alternative to transcript compaction: keep each session short and hand off
   only what matters. Verified live: a fresh-workspace session continued
   correctly from a brief alone.
3. **Output style / verbosity** (`-style`, built). `normal` | `terse` |
   `caveman` injects an output-compression directive; terser output means fewer
   *output* tokens (the expensive kind) while code, commands, and errors stay
   verbatim. A knob on the agent, not a global.

Roadmap (same pillar):

4. **Auto-compact via rolling handoff.** When a single run's turns/tokens cross
   a threshold, generate a brief mid-run, discard the session, and restart from
   the brief — provider-agnostic rolling compaction reusing the brief machinery.
5. **Tool-output windowing.** Cap and stub large `read`/`bash` outputs in
   history after they've been consumed, so a big result doesn't re-bill every
   subsequent turn.
6. **Native Anthropic context editing** for the anthropic provider
   (`clear_tool_uses`, compaction beta) where available — a provider-specific
   accelerant under the same interface.

The through-line: every token not sent is a token not paid for, and on the local
tier the cheapest token is the one handled by the GPU. Context economy and the
cost stack are the same objective from two angles.

## The router (the intelligence)

Everything above is plumbing that exists in every agent; the router is what
makes holocron holocron. Start with an explicit, inspectable policy — not a
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

## Provider interface (replaces the stub)

```go
type Provider interface {
    // Stream a completion; emits text/thinking deltas and tool calls.
    Stream(ctx context.Context, req Request) (<-chan Event, error)
    // Model metadata: context window, pricing, tool support, effort levels.
    Models() []ModelInfo
}
```

Implementations: `anthropic` (official Go SDK,
`github.com/anthropics/anthropic-sdk-go` — streaming + `toolrunner` for the agent
loop + `CacheControl` on system blocks + effort), `ollama` (OpenAI-compatible
`/v1` against the in-cluster service). The existing `Manager` keeps its
register/lookup shape but gains the router in front of it. The stub
`gemini`/`codex`/`antigravity` files are deleted unless a real provider is added.

## Tools

Tool quality shapes agent behavior more than model choice within a tier. Day-one
set, correctness over breadth:

- `read` (line-numbered, range, refuses huge files)
- `edit` (exact-match replacement, unique-match required, legible failure — this
  one tool disproportionately determines how good the agent feels)
- `write`, `bash` (permission-gated), `grep`/`glob`
- `screenshot_publish` — homelab-specific: headless capture (the opencode-server
  image already runs Xvfb + llvmpipe) → artifact store → shareable link. Serves
  the GoQuest loop directly.

MCP client support (stdio + http) so the existing `rd-local` and
`ollama-delegate`-style servers plug in.

## Permission modes (Claude Code parity)

Four modes as presets over a per-tool ruleset (`allow`/`ask`/`deny`):

| Mode | Policy |
|---|---|
| Manual | every tool `ask` |
| Auto | every tool `allow` |
| Accept edits | edit/write `allow`, bash/external `ask` |
| Plan | lead runs read-only, produces a plan, no mutations until approved |

Plan mode is a lead constraint (read-only tool set + plan output), not just a
ruleset. Live `ask` prompts flow over SSE to the phone and back — that is the
push-notification path.

## Clients — the Crush feel without the fork

Crush's look comes from Charm's **public, MIT-licensed** libraries; only Crush's
agent logic is walled in `internal/`. So holocron gets the entire Crush aesthetic
with zero fork by building a TUI on those libraries:

- `github.com/charmbracelet/bubbletea` — TUI framework (Elm architecture)
- `github.com/charmbracelet/bubbles` — components (viewport, textinput, spinner, list)
- `github.com/charmbracelet/lipgloss` — styling/layout
- `github.com/charmbracelet/glamour` — in-terminal markdown rendering

Three clients, one core, all over the same HTTP+SSE API:

1. **Bubble Tea TUI** (`cmd/holocron-tui` or the existing `cmd/`) — the Crush-feel
   local client.
2. **Existing Vue web app** — kept; it already speaks the SSE + `/api/command` +
   `/api/upload` surface.
3. **iPhone app** — later, generated against the same API.

The server stays client-agnostic; the TUI is a consumer, not the core. (The
design doc's earlier "server-first" note stands — the TUI is additive.)

## Remote control via `holocron serve`

The phone app must drive sessions the way Claude Code's Remote Control does: fire
a task, put the phone away, the run keeps going, reattach later to watch, steer,
or approve. But "remote control" is a property of *where the session lives*, not
of an outbound socket. A long-running server that owns the sessions delivers it
directly, with clients connecting inbound — the `opencode serve` / `crush serve`
model. So holocron's entrypoint is a **`serve` subcommand** (formalizing the
current `cmd/holocron/main.go`, which already just runs the server), not a
Claude-Code-style outbound `remote-control` dial-out. No agent-initiated tunnel,
no separate control plane — one process holding the sessions and the API.

```
holocron serve --addr :8080        # holds sessions, serves HTTP+SSE
```

What makes it "remote control" is the session model, unchanged by dropping the
dial-out:

- **Sessions are server-side and client-independent.** A run continues to
  completion in-cluster whether or not any client is connected. `POST
  /sessions/{id}/prompt` is async (returns immediately); the loop runs inside
  `serve`. Closing the phone never pauses the agent.
- **Any client attaches to any session by ID.** Reconnecting fetches full history
  (`GET /sessions/{id}/events`) then tails live (`GET /events` SSE), deduping by
  event id. TUI, web, and phone are peers — start a run in the TUI, reattach from
  the phone, finish in the web UI.
- **Steering works detached.** Interrupt, queued follow-up messages, permission
  replies, and mode switches are events sent to the session, processed in order,
  independent of which client (if any) is watching.
- **Lossless reconnect.** SSE has no replay, so every (re)connect overlaps a
  history fetch with the live stream and dedupes — a permission ask that fired
  while the phone was backgrounded is still waiting on reattach, not lost.

Exposure is the same posture as everything else: `holocron serve` stays behind
Cloudflare Access + the bearer on the one public hostname; inbound only, no
special tunnel to run. (If zero public ingress were ever required, an outbound
relay could be added later without touching the session model — but it is not the
default and not needed here.)

iOS specifics: background URLSession streams get suspended, so a long run won't
stream while the app is backgrounded — use `prompt` async + reconcile on
foreground, and let the **push notification** (permission ask / idle / error) be
the signal to reattach. That push path is the same SSE-consumer-to-APNs relay in
the delivery layer.

## Server API (own it, design it for the phone)

Building the agent ourselves means we define the client API instead of
reverse-engineering one. Rough surface, extending what's already there:

- `POST /sessions`, `GET /sessions`, `GET /sessions/{id}`
- `POST /sessions/{id}/prompt` (async; returns immediately)
- `GET /sessions/{id}/events`, `GET /events` (SSE — already built)
- `POST /sessions/{id}/permission/{id}/reply`
- `POST /artifacts`, `GET /a/{id}` — shareable capability links (screenshots, gifs)
- `POST /upload` — already built (`internal/assets`)
- `PUT/DELETE /devices/{token}` — APNs registration
- `GET /cost/{sessionID}` — the tier/cost breakdown, a headline feature

Auth: static bearer + Cloudflare Access in front, matching the homelab posture.

## Delivery layer — already written

Push, artifact links, upload, and APNs were built and tested as a standalone Go
relay this session (`~/Projects/opencode-relay`). Those files have **zero backend
coupling** and move into holocron's server layer unchanged:

- `apns.go` — ES256 JWT + HTTP/2 APNs (stdlib only; JWT verifies in tests)
- `artifacts.go` — capability-URL media store
- `devices.go` — device token store
- `crypto.go`, auth middleware, config pattern

Only the opencode-specific proxy/event-map files are dropped. The delivery layer
is done; holocron inherits it.

## Deployment

Distroless static Go binary, pushed to `registry.farmerjohnparmesan.com`, base +
overlay under `kubernetes/` in the `k8s-homelab` repo (the existing `deploy/k8s`
manifests here are a starting point to migrate into that pattern). Needs
in-cluster reachability to ollama (already a Service), egress to 443 for
Anthropic, and an iSCSI PVC for session/artifact/device state. APNs `.p8` in
SOPS. Cloudflare Access on the one public hostname; opencode-server, if kept,
stays LAN-only.

## Build phases

Staged so there's a dogfoodable thing early, with a gate that can kill the
thesis before it's expensive.

1. **Real Anthropic provider + loop.** Replace the stub `claude.go` with the Go
   SDK; agent loop with `read`/`edit`/`bash`; single model. Prove it edits code
   correctly.
2. **Add ollama + manual routing.** Second provider; user picks lead/worker per
   session. Proves the provider abstraction.
3. **The router + cost stack.** Lead decomposes → policy assigns → local worker
   executes → verify → escalate. Prompt caching, effort routing, cost accounting.
   **Gate: if the router doesn't cut cost while holding quality, fold back to
   wrapping opencode.** Everything before this is table stakes; this is the
   milestone that justifies the project.
4. **Permission modes + plan mode.**
5. **Bubble Tea TUI** on the HTTP+SSE API.
6. **Delivery** (drop in the relay files) + iPhone app.

## Risks

- **Tool-layer quality** is a moat you still cross even with a perfect router; a
  flaky `edit` makes the agent feel bad. Keep phase 1 small and correct.
- **Worker reliability** — the verify/escalate loop is load-bearing; weak
  verification lets bad local output poison runs. Test in phase 3.
- **Routing overhead** — decomposing every task costs lead tokens; the fast-path
  is required, not optional.
- **Self-contained prompts** — getting the lead to emit genuinely self-contained
  worker prompts is itself a prompt-engineering problem.
- **Solo maintenance** — a real ongoing project vs. bumping an image tag. Eyes
  open; the phase-3 gate is the off-ramp.
