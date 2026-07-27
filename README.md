# Holocron

A cost-aware, multi-model coding agent. A frontier model plans and reasons; a
cheaper model — down to a free local GPU model — does the bounded work; the
agent routes each unit of work to the cheapest model that can do it correctly,
and reports what it cost. Drivable from a terminal (Charm TUI), a phone, or the
HTTP API.

Written in Go. MIT licensed. See [`DESIGN.md`](DESIGN.md) for the full design.

## Why

The differentiator is **cost-aware multi-model orchestration**. A coding task is
a tree of subtasks of wildly varying difficulty; the expensive model should only
touch the hard nodes. Holocron makes that routing the product, and treats
**context economy** — spending the fewest tokens per turn — as a first-class
concern.

## Install / build

Go 1.26+. No cgo.

```bash
go build -o holocron ./cmd/holocron
go test ./...
```

## Subcommands

```
holocron agent  [flags] <prompt...>   # run the agent once against a workspace
holocron tui    [flags]               # interactive terminal client
holocron serve  [flags]               # HTTP + SSE server (sessions, remote control)
holocron brief  [name]                # print a saved handoff brief, or list sessions
```

### agent

Runs a coding agent with `read` / `write` / `edit` / `bash` tools, confined to a
workspace directory.

```bash
# Frontier model:
ANTHROPIC_API_KEY=... holocron agent --model claude-opus-5 -C ./repo "add a --json flag"

# Local model via any OpenAI-compatible endpoint (e.g. ollama):
holocron agent --provider openai --base-url https://ollama.example.com/v1 \
  --model qwen3-coder:30b -C ./repo "fix the failing test"
```

Key flags: `--provider` (`anthropic` | `openai`), `--model`, `--base-url`,
`--api-key`, `-C` (workspace), `--style`, `--session`, and the worker flags below.

### Providers

Backends self-register (driver style). Three ship today:

- **`anthropic`** — the official Anthropic Go SDK. Prompt caching on the system
  prompt, adaptive thinking, per-turn usage.
- **`openai`** — any OpenAI-compatible `/chat/completions` endpoint: OpenAI
  itself, or a local **ollama** server (point `--base-url` at its `/v1`). This is
  the free local tier.
- **`bedrock`** — Anthropic models on **AWS Bedrock**, speaking the same
  Messages API over Bedrock's transport (shares the anthropic session logic).
  Credentials and region come from the standard AWS default chain
  (`AWS_REGION`/`AWS_DEFAULT_REGION`, access keys, `AWS_PROFILE`, SSO, instance
  roles). Pass a Bedrock model id or inference-profile id as `--model`, e.g.
  `us.anthropic.claude-opus-4-5-20251101-v1:0`.

Credentials: `ANTHROPIC_API_KEY` (or an `ant auth login` profile) for Anthropic;
`--api-key` / `--base-url` for OpenAI-compatible; the AWS chain for Bedrock. See
**Configuration** below for env vars and the config file.

## Configuration

Provider settings resolve from three layers, in increasing precedence: a JSON
config file, environment variables, then command flags. An explicitly-set flag
always wins; otherwise an env var beats the file, which beats the built-in
default.

**Environment variables** (the ones other tools honor):

| Provider | Base URL | API key / credentials |
|---|---|---|
| `anthropic` | `ANTHROPIC_BASE_URL` | `ANTHROPIC_API_KEY` |
| `openai` | `OPENAI_BASE_URL` (or `OPENAI_API_BASE`) | `OPENAI_API_KEY` |
| `bedrock` | `ANTHROPIC_BEDROCK_BASE_URL` (optional) | AWS default chain (`AWS_REGION`, `AWS_PROFILE`, ...) |

**Config file** (JSON), searched in order: `--config <path>`,
`$HOLOCRON_CONFIG`, `./holocron.json`, `~/.holocron/settings.json`, then the
user config dir (`.../holocron/config.json`). First one found wins. Shape:

```json
{
  "provider": "anthropic",
  "model": "claude-opus-5",
  "base_url": "",
  "api_key": "",
  "style": "normal",
  "worker": {
    "provider": "openai",
    "model": "qwen3-coder:30b",
    "base_url": "https://ollama.example.com/v1"
  }
}
```

The `worker` block enables cost-aware delegation (below) without any flags.

## Cost-aware delegation (lead + worker)

Give the agent a cheaper **worker** model and it gains a `delegate` tool: the
lead hands bounded, self-contained subtasks to the worker (which returns a
conclusion, not file dumps), keeping expensive lead context small. Usage is
tracked per model, so the run's cost reflects the split — and the free local
tier shows `$0`.

```bash
holocron agent --model claude-opus-5 \
  --worker-provider openai --worker-base-url https://ollama.example.com/v1 \
  --worker-model qwen3-coder:30b \
  -C ./repo "refactor the config loader and delegate the boilerplate"
```

Every run prints a per-model token/cost breakdown and a total.

## Code review skill (separate reviewer model)

Give the agent a **reviewer** model and, after a run makes file changes, that
model reviews the collected diffs — a second opinion from a different (often
stronger) model than did the edits. Findings are printed at the end of the run,
emitted as a `review` event for clients, and returned in `Result.Review`. The
reviewer's usage is tracked under its own model, so its cost shows in the
breakdown.

```bash
holocron agent --model qwen3-coder:30b --provider openai --base-url https://ollama.example.com/v1 \
  --reviewer-provider anthropic --reviewer-model claude-opus-5 \
  -C ./repo "add input validation to the config loader"
# ... run output ...
# --- code review (claude-opus-5) ---
# config.go: the new length check uses < where it should be <=; off-by-one …
```

Or configure it once in the config file:

```json
{ "reviewer": { "provider": "anthropic", "model": "claude-opus-5" } }
```

The reviewer only runs when at least one file was written/edited.

## Context economy

Keeping context small is a first-class feature (fewer tokens = lower cost):

- **Handoff brief + resume** (`--session <name>`). A session ends by writing a
  compact brief (task, state, files, next steps, gotchas); a new session resumes
  from that brief, not the full transcript. Cheaper than transcript compaction.
  Inspect with `holocron brief <name>`.
- **Delegation** returns conclusions, not dumps (above).
- **Output style** (`--style normal|terse|caveman`) compresses the agent's own
  output; code, commands, and errors stay verbatim.

```bash
holocron agent --session refactor -C ./repo "start the config refactor"
# ... later, in a fresh run:
holocron agent --session refactor -C ./repo "continue where we left off"
holocron brief refactor            # view the saved handoff
```

## TUI

Interactive terminal client built on Charm's Bubble Tea / Lip Gloss / Glamour —
dark base, Carolina-blue accent, block logo. Streams the agent's events (tool
calls, delegation, cost) live.

```bash
holocron tui --provider openai --base-url https://ollama.example.com/v1 --model qwen3-coder:30b -C ./repo
```

`--logo N` selects the logo (0 filled, 1 gradient (default), 2 boxed, 3 wordmark).

## Benchmarking

`holocron bench` runs the same objectively-checkable coding tasks through
holocron and — if installed — **Claude Code** (`claude -p`) and **Codex**
(`codex exec`), in fresh isolated workspaces, and reports wall-clock time and
task success. Success is graded by a check command that must exit 0 (e.g.
`go test`), not by self-report. Holocron additionally reports its token cost;
the external CLIs don't expose that, so they show `-`.

```bash
holocron bench --provider openai --base-url https://ollama.example.com/v1 --model qwen3-coder:30b

running 2 tasks across 3 runners...

TASK             RUNNER       OK       TIME       COST       NOTES
add-function     holocron     yes      14.3s      $0.0000
edit-output      holocron     yes      13.2s      $0.0000
...
```

Missing CLIs are skipped with a note. Tasks live in `internal/bench`.

## The publish tool

When a worker/serve session is configured with an artifact store, the agent
gains a `publish(path)` tool: it shares a produced file (screenshot, gif,
report) at a capability URL and hands the link back, so a run that renders
something can return a shareable link inline. Enabled automatically under
`holocron serve` (backed by the artifact store).

## serve — remote control

`holocron serve` holds **server-side sessions** that clients (TUI, web, phone)
attach to over SSE. A run continues to completion whether or not any client is
connected — the remote-control property. Reconnect is lossless: every event
carries a per-session monotonic `seq`, so a client fetches history then tails
live, deduping by seq.

```bash
holocron serve --addr :8080 \
  --provider openai --base-url https://ollama.example.com/v1 --model qwen3-coder:30b \
  --worker-base-url https://ollama.example.com/v1 --worker-model qwen3-coder:30b \
  -C ./workspace
```

### API

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/health` | liveness (always open) |
| GET | `/api/providers` · `/api/whoami` | backend info · caller identity |
| GET | `/api/sessions` | list sessions (id, model, status, cost) |
| POST | `/api/sessions` | create — body `{model?, worker_model?, style?}` |
| GET | `/api/sessions/{id}` | session status + cost |
| POST | `/api/sessions/{id}/prompt` | **async** run — body `{prompt}`, returns 202 |
| POST | `/api/sessions/{id}/interrupt` | cancel a running prompt |
| GET | `/api/sessions/{id}/events?after=N` | event history (for reconnect) |
| GET | `/api/sessions/{id}/stream` | SSE live tail (honors `?after=` / `Last-Event-ID`) |
| POST | `/api/upload` | ingest a file into the workspace (agent can use it) |
| POST | `/api/artifacts` | publish a workspace file — body `{path}`, returns `{url, id, mime}` |
| GET | `/a/{id}` | serve a published artifact (unauth — the id is the capability) |

### Artifacts (upload / share)

Two directions, like Claude Code:

- **Upload in** — `POST /api/upload` (multipart `asset`) writes the file under
  `uploads/<name>` in the workspace, so the agent can read it. Returns its path.
- **Share out** — `POST /api/artifacts {"path":"shot.png"}` copies a workspace
  file into the artifact store and returns a shareable capability URL
  (`/a/<id>`). `GET /a/<id>` serves it inline (renders images/gifs in a browser)
  and needs no auth — the unguessable id *is* the credential, so a link can be
  sent to someone without an account. Set `--public-url` for absolute links.

## Auth (SSO)

`serve` runs open by default (logs a warning). Configure any of three modes to
enforce SSO — all gated by an optional email allowlist, verified with `go-oidc`
(JWT crypto is never hand-rolled):

- **OIDC login → cookie** (browser): `/auth/login` → your IdP → `/auth/callback`
  (authorization-code + PKCE + nonce), HMAC-signed session cookie. Works with any
  OIDC provider — Keycloak, Google, Microsoft, Okta, Auth0.
- **Bearer token** (TUI / phone / CLI): an OIDC id_token, or a static API token.
- **Cloudflare Access** (behind cloudflared + Access): verifies the
  `Cf-Access-Jwt-Assertion` header against the Access issuer + AUD.

`/api/health` stays open for probes; every other `/api` route requires auth.

```bash
# OIDC (Keycloak):
holocron serve --addr :8080 \
  --auth-issuer https://keycloak.example.com/realms/homelab \
  --auth-client-id holocron --auth-client-secret ... \
  --auth-redirect-url https://holocron.example.com/auth/callback \
  --auth-allowed-emails you@example.com

# Cloudflare Access:
holocron serve --auth-cf-team https://team.cloudflareaccess.com --auth-cf-aud <AUD>

# Static token (programmatic clients):
holocron serve --auth-token "$(openssl rand -hex 32)"
```

Auth flags: `--auth-issuer --auth-client-id --auth-client-secret --auth-redirect-url
-auth-allowed-emails --auth-cf-team --auth-cf-aud --auth-token --auth-cookie-secret
-auth-insecure-cookies`.

## Layout

```
cmd/holocron            subcommands: agent · tui · serve · brief
internal/agent          importable agent: tool loop, workspace, delegation, cost, brief
internal/provider       model-agnostic registry (driver-style init())
  ├── anthropic         Anthropic Go SDK + prompt caching
  ├── bedrock           Anthropic on AWS Bedrock (reuses anthropic session)
  └── openai            OpenAI-compatible (OpenAI, ollama)
internal/config         provider config: JSON file + env var resolution
internal/server         serve: session manager, SSE, HTTP API
internal/auth           SSO: OIDC login, bearer, Cloudflare Access
internal/artifact       capability-URL store (publish/share files)
internal/session        handoff briefs (persisted)
internal/storage        session/brief file store
internal/tui            Bubble Tea terminal client
internal/bench          comparative benchmark harness (vs Claude Code, Codex)
mobile/                 Flutter client (iOS + Android) — see mobile/README.md
frontend/               Vue web client
```

## Tests

```bash
go test ./...            # every package
go test -cover ./internal/...
```
