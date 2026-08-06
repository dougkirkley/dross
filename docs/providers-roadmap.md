# Provider parity roadmap

Goal: support roughly the same breadth of model providers as **OpenCode** and
**Crush** (both source their catalog from [Catwalk](https://github.com/charmbracelet/catwalk)
/ [models.dev](https://models.dev) — ~75 providers). dross already has a
`provider.Provider` registry (`internal/provider`) with `anthropic`, `openai`
(OpenAI-compatible, covers ollama/LM Studio/llama.cpp via `base_url`), `bedrock`,
and `claude-code`. Most remaining providers are OpenAI-compatible,
so the work is mostly a **catalog + a few native adapters + auth helpers**, not 75
bespoke clients.

## Phase 1 — Model catalog (highest leverage)
- [ ] Integrate a models.dev/Catwalk-style catalog: provider id, base URL,
      model ids, context window, pricing (in/out), capabilities (tools, vision,
      reasoning). Ship it embedded + refreshable.
- [ ] Drive `/model`, `/usage` context bars, and cost accounting from the
      catalog instead of the hardcoded `contextWindow`/pricing tables.
- [ ] `provider: "<catalog-id>"` in config auto-fills base URL + known models.

## Phase 2 — OpenAI-compatible providers (config only; catalog entries)
These need only a catalog entry pointing the existing `openai` provider at the
right base URL (+ API key env). No new Go code.
- [ ] Groq, Cerebras, DeepSeek, xAI (Grok), Mistral (OpenAI-compat endpoint),
      OpenRouter, Together, Fireworks, DeepInfra, Perplexity, Nebius, Novita,
      Hyperbolic, Baseten, Venice, Requesty, Moonshot (Kimi), Zhipu (GLM),
      Inception, Morph, v0, GitHub Models, Hugging Face Inference,
      Azure OpenAI (deployment-based URL), LiteLLM proxies.

## Phase 3 — Native (non-OpenAI-compatible) adapters (new Go backends)
- [ ] Google Gemini (AI Studio) — native `generateContent` + tool calling.
- [ ] Google Vertex AI — Gemini + Claude on Vertex (auth via ADC).
- [ ] Cohere — native chat/tools API (if not adequately OpenAI-compat).
- [ ] Amazon Bedrock — extend existing `bedrock` to more model families.

## Phase 4 — Auth helpers
- [ ] GitHub Copilot — OAuth device flow → token, OpenAI-compat endpoint.
- [ ] AWS Bedrock — standard credential chain (partly done).
- [ ] Vertex — application default credentials.
- [ ] Per-provider API-key env var conventions (extend `config.EnvFor`).

## Phase 5 — Quality / parity polish
- [ ] Verify tool-calling works per provider (some emit XML like qwen — the
      openai provider's XML fallback already handles that).
- [ ] Reasoning/thinking params per provider.
- [ ] Streaming for the openai provider (currently non-streaming Send).
- [ ] Tests: a table-driven provider conformance suite (tool call, tool result
      round-trip, usage, stop reasons) run against mock servers.

## Notes
- The `openai` provider + `base_url` already unlocks the majority of providers
  today; Phase 1+2 is mostly data.
- Keep the driver-style registry: each native backend registers in `init()` and
  is enabled by blank import in `cmd/dross/main.go`.
- Consider consuming Catwalk directly (it's MIT, community-maintained) rather
  than hand-maintaining the catalog.
