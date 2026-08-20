# Aperture

**Self-hosted DLP gateway for AI agents.** A drop-in proxy between your
applications/agents and LLM providers (OpenAI, Anthropic, Groq) that scans
every request for secrets, PII and custom stop-patterns — **before it leaves
your network**.

Your agents talk to the cloud. Know what they say.

![DLP Events — incident feed](docs/screenshots/dlp-events.png)

- **Block or redact** AWS keys, GitHub/GitLab/Slack tokens, private keys, JWTs, emails, credit cards, phones, IBANs — plus your own regex rules
- **Scans the whole request**: prompts, system prompt, multimodal text, tool-call arguments and tool results — not just the visible message
- **Scans responses too** (opt-in): a model echoing a secret back is caught mid-stream, across chunk boundaries
- **Incident feed**: who sent what, when — with masked samples (raw sensitive content is never stored)
- **Per-key policies** with hot reload and a dry-run API ("what would happen to this text")
- **Audit report**: after a week in alert mode, see what `block` would have stopped — then flip the switch
- **Webhook alerts** to Slack/Telegram/anything, with storm debounce — configurable from the console
- **Budgets & rate limits** per key — a looping agent gets `429`, not your monthly spend
- **Cost & token tracking** per model, key and agent
- **Prometheus metrics** at `/metrics` — traffic, spend and DLP events on your existing dashboards
- **Works with coding agents**: speaks the OpenAI Chat Completions and Responses APIs, plus the native Anthropic Messages API
- Single Go binary: point your agent at it by changing `base_url`

```
 agents / apps ──► Aperture (scan · block · redact · log) ──► OpenAI / Anthropic / Groq
```

## Quickstart: first caught secret in 2 minutes

```bash
docker run -p 8080:8080 -e OPENAI_API_KEY=sk-... ghcr.io/danilovid/aperture:latest
# (or build from source: docker build -t aperture . && docker run -p 8080:8080 -e OPENAI_API_KEY=sk-... aperture)
# The log prints your generated APERTURE_API_KEY and ADMIN_API_KEY.

curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer <APERTURE_API_KEY>" -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"deploy with AKIAIOSFODNN7EXAMPLE"}]}'
# → 403 {"error":{"type":"aperture_dlp_blocked","rules":["aws-access-key"],...}}

curl -H "Authorization: Bearer <ADMIN_API_KEY>" http://localhost:8080/admin/dlp/events
# → the incident, with a masked sample: "AKIA****************"
```

Clean traffic passes through untouched (streaming included); PII is redacted
in place — the provider receives `[REDACTED:email]` instead of the address.

### Protecting Claude Code

Aperture also serves the native Anthropic Messages API, so Anthropic clients
work by pointing them at the gateway — one env var, no code change:

```bash
export ANTHROPIC_BASE_URL=http://localhost:8080
export ANTHROPIC_API_KEY=<APERTURE_API_KEY>   # your aperture key, not the Anthropic one
claude
```

Everything the agent sends — prompts, system prompt, tool arguments and tool
results (where a file it just read ends up) — is scanned before it leaves your
network. Streaming is passed through untouched.

More: [`examples/`](examples) — curl, OpenAI Python/Node SDKs, pointing
coding agents at the gateway, demo seeding.

## Web console

```bash
cd web && npm ci && npm run dev   # http://localhost:5173
# ⚙ Settings → paste the admin & aperture keys from the server log
```

Overview (traffic + DLP KPIs), DLP Events (filterable incident feed),
Policies (per-key detector toggles with live dry-run preview), Report ("what
would have been blocked", exportable), Settings & Keys — including budgets,
rate limits and webhook alerts with a "send test alert" button, so the whole
setup is doable without curl.

![Policies — live dry-run](docs/screenshots/policies.png)

## Running for real

**With PostgreSQL** — keys, policies and incidents survive restarts:
```bash
docker compose up -d    # postgres + gateway + console
# or manually:
export DATABASE_URL=postgres://aperture:aperture@localhost:5432/aperture?sslmode=disable
export ADMIN_API_KEY=your-admin-secret
export APERTURE_ENCRYPTION_KEY=$(openssl rand -hex 32)   # AES-256-GCM at rest
go run ./cmd/aperture
```

Create per-team keys (returned once, stored as sha256):
```bash
curl -X POST http://localhost:8080/admin/keys \
  -H "Authorization: Bearer $ADMIN_API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"ci-agent","openai_api_key":"sk-..."}'
```

## Environment variables

| Variable | Meaning |
|----------|---------|
| `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `GROQ_API_KEY` | Provider keys, seeded on startup in no-DB mode |
| `APERTURE_API_KEY` | Bearer token clients use (generated & logged if unset) |
| `ADMIN_API_KEY` | Token for `/admin/*` (generated & logged if unset; admin is never open) |
| `DATABASE_URL` | PostgreSQL: keys, policies, DLP events persist |
| `APERTURE_ENCRYPTION_KEY` | 64 hex chars — AES-256-GCM for provider keys at rest (`openssl rand -hex 32`). Aperture keys are always stored hashed |
| `DLP_ENABLED` | Outbound scanning (default `true`) |
| `DLP_SECRETS_ACTION` / `DLP_PII_ACTION` / `DLP_CUSTOM_ACTION` | `off\|alert\|redact\|block` (defaults: `block` / `redact` / `alert`) |
| `DLP_SCAN_RESPONSES` | Also scan what the model sends back (default `false`) |
| `DLP_WEBHOOK_URL` / `DLP_WEBHOOK_FORMAT` / `DLP_WEBHOOK_ACTIONS` / `DLP_WEBHOOK_CHAT_ID` | Alerts: `json`/`slack`/`telegram`, actions filter (default `blocked`) |
| `OPENAI_BASE_URL` | Override upstream (default `https://api.openai.com`) |
| `ANTHROPIC_BASE_URL` | Override upstream for `/v1/messages` (default `https://api.anthropic.com`) |
| `CUSTOM_PROVIDERS` | JSON array of custom OpenAI-compatible upstreams (DeepSeek, Qwen, Ollama, private endpoints) — see below |
| `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` | Route upstream provider calls through a corporate egress proxy (standard Go proxy env vars) |
| `ALLOWED_ORIGINS` | CORS allowlist (default: localhost dev origins) |
| `LIMIT_BUDGET_DAILY_USD` | Default daily spend ceiling per key (empty = no limit) |
| `LIMIT_REQUESTS_PER_MINUTE` | Default request rate ceiling per key (empty = no limit) |
| `PORT` | Listen port (default `8080`) |

Provider is selected by model name: `claude*` → Anthropic, `llama*`/`mixtral*` → Groq, everything else → OpenAI.

### Custom providers

Route any OpenAI-compatible endpoint (DeepSeek, Qwen/DashScope, Moonshot, GLM,
a local Ollama/vLLM, or a private gateway) by model prefix. `base_url` must
already include the version segment; custom prefixes are matched **before** the
built-ins.

```bash
export CUSTOM_PROVIDERS='[
  {"name":"deepseek","base_url":"https://api.deepseek.com/v1","prefixes":["deepseek"],"api_key":"sk-..."},
  {"name":"qwen","base_url":"https://dashscope.aliyuncs.com/compatible-mode/v1","prefixes":["qwen"],"api_key":"sk-..."},
  {"name":"ollama","base_url":"http://localhost:11434/v1","prefixes":["mistral","gemma"],"api_key":"ollama"}
]'
# then: {"model":"deepseek-chat", ...} is scanned by DLP and proxied to DeepSeek.
```

Local endpoints (Ollama, vLLM) ignore auth — set any placeholder `api_key` so
the provider stays configured. Every custom-provider request is scanned by DLP
and attributed to the provider name in the incident feed and stats.

## API

| Path | Description |
|------|-------------|
| `POST /v1/chat/completions` | OpenAI-compatible chat (Bearer: aperture_key); scanned by DLP |
| `POST /v1/messages` | Native Anthropic Messages API (`x-api-key` or Bearer: aperture_key); scanned by DLP |
| `POST /v1/responses` | OpenAI Responses API (Bearer: aperture_key); scanned by DLP |
| `GET /v1/models` | List models (Bearer: aperture_key) |
| `GET /admin/dlp/events` | Incident feed; filters: action, rule, key_id, agent, session, limit, period |
| `GET /admin/dlp/summary` | Blocked/redacted/alerted counters for a period |
| `GET /admin/dlp/report` | Audit report: what enabling `block` would have stopped (`period=24h\|7d\|30d`) |
| `GET/PUT /admin/policies…` | Default & per-key policies, hot-applied; `POST /admin/policies/test` dry-run |
| `POST /admin/policies/keys/{id}/mute` | Silence one detector for a key (and `/unmute`) |
| `GET/PUT /admin/limits…` | Default & per-key budgets and rate limits, plus today's spend |
| `GET/PUT /admin/alerts` | Webhook alert config (URL masked on read); `POST /admin/alerts/test` |
| `GET/POST/DELETE /admin/keys…` | Aperture key management (PostgreSQL) |
| `GET/POST/DELETE /admin/config` | Provider keys for the default key |
| `GET /admin/stats/…` | Requests/tokens/cost/latency (PostgreSQL) |
| `GET /health` · `GET /ready` | Liveness · readiness (pings PostgreSQL when configured) |
| `GET /metrics` | Prometheus metrics (unauthenticated — carries no key material) |

All `/admin/*` routes require `Authorization: Bearer <ADMIN_API_KEY>`.

A policy maps detector groups to actions, plus custom rules and false-positive
controls:
```json
{"secrets":"block","pii":"redact","custom":"alert",
 "custom_rules":[{"name":"project-x","pattern":"project-x"}],
 "allowlist":["AKIAIOSFODNN7EXAMPLE"],
 "muted_rules":["email"],
 "scan_responses":false}
```

**Budgets and rate limits.** Give a key a daily spend ceiling and a request
rate, so a looping agent cannot burn the month's budget overnight. Over the
limit the gateway answers `429` with `Retry-After`, the upstream is never
called, and the cut-off lands in the incident feed and the webhook alert (once
per key per day, not per rejected request):

```bash
curl -X PUT http://localhost:8080/admin/limits/keys/<KEY_ID> \
  -H "Authorization: Bearer $ADMIN_API_KEY" -H "Content-Type: application/json" \
  -d '{"budget_daily_usd": 10, "requests_per_minute": 60}'
```

Budgets reset at 00:00 UTC and today's spend is recovered from the request log
on restart, so a restart does not hand a key a fresh budget. Counters are
per-instance: behind a load balancer each instance enforces its own share.

**Attribution.** Several agents usually share one key, so send
`X-Aperture-Agent` and `X-Aperture-Session` on `/v1/*` requests to tell them
apart. Both are optional and land on incidents and usage rows, so the feed and
the cost figures can be split per agent or per run:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $APERTURE_API_KEY" \
  -H "X-Aperture-Agent: ci-bot" -H "X-Aperture-Session: build-4821" \
  -H "Content-Type: application/json" -d '{...}'
```

**False positives.** The first bad block is what makes a team switch DLP off, so
two escape hatches exist: `allowlist` (patterns whose matches never raise a
finding — AWS's documented example key is the classic case) and `muted_rules`
(a detector silenced for one key, one click from the incident feed). Neither is
silent: suppressed matches are still recorded as `suppressed` and counted in
`/admin/dlp/summary`.

**Scanning responses.** By default Aperture inspects what leaves your network.
A model can also hand a secret *back* — echoing a credential it was shown, or
putting one in a tool call the agent then runs. Set `scan_responses` on a
policy (or `DLP_SCAN_RESPONSES=true`) and the same detectors, with the same
per-group actions, apply to the answer:

```bash
curl -X PUT http://localhost:8080/admin/policies/default \
  -H "Authorization: Bearer $ADMIN_API_KEY" -H "Content-Type: application/json" \
  -d '{"secrets":"redact","pii":"redact","custom":"alert","scan_responses":true}'
```

Streaming is the hard part, and it is handled: the answer is scanned through a
sliding window, so a key split across three SSE chunks is still caught. Under
`redact` the text is rewritten in flight; under `block` the stream is torn down
at the first match and the client gets an in-band error event
(`aperture_dlp_blocked`) instead of a truncated answer. Non-streaming responses
are rejected with `403`. Tool-call arguments are scanned as their own channel,
and the Responses API's terminal events — which repeat the full text — are
rewritten too, so no client path reassembles what the deltas hid.

The cost is a fixed lag, not a slower answer: the client sees the first token
once **256 bytes** have arrived (measured: 0.88 s into a 1.46 s answer, which
still finished at 1.46 s). Nothing is buffered beyond the window. That lag is
why this is off by default.

**Before you switch to block.** Nobody flips a DLP gateway to `block` blind.
The path is: run in `alert` for a week, read the report, then decide. That
report is one endpoint — and the console's **Report** tab, with Markdown and
JSON export:

```bash
curl -H "Authorization: Bearer $ADMIN_API_KEY" \
  "http://localhost:8080/admin/dlp/report?period=7d"
```

It answers per detector group how many requests `block` would have rejected,
which rules and keys they belong to, which agent sent them, and what policy let
them through. Requests already blocked, and matches silenced by a mute or an
allowlist entry, are excluded — those are not a change.

**Prometheus metrics.** `GET /metrics` exposes the gateway in the Prometheus
text format, so traffic, spend and DLP activity land on the same dashboards as
the rest of your infrastructure:

```
aperture_http_requests_total{path,status}          aperture_tokens_total{direction}
aperture_http_request_duration_seconds{le}         aperture_cost_usd_total{provider}
aperture_llm_requests_total{provider,model,status} aperture_dlp_events_total{rule,action}
aperture_limit_denied_total{reason}
```

```yaml
scrape_configs:
  - job_name: aperture
    static_configs: [{targets: ["localhost:8080"]}]
```

The endpoint needs no token and never exposes key material — labels are route
patterns (`/admin/keys/{id}`), never raw paths, so ids stay out of the label
set and the series count stays bounded. It is still an operational surface:
keep it on an internal network, or let your reverse proxy gate `/metrics`.

## What Aperture does not do

- Does not scan browser traffic to ChatGPT/Claude web UIs — it protects the
  **API path** (agents, SDKs, backends). For browser DLP look at enterprise
  CASB tooling.
- Does not store raw sensitive content anywhere — events keep a masked sample
  only.
- Does not scan responses unless you turn it on per policy (`scan_responses`)
  — the default protects the outbound path only.

## Documentation

- [Architecture](docs/ARCHITECTURE.md)
- [Roadmap](docs/ROADMAP.md)
- [Auth and roles](docs/AUTH_AND_ACCESS.md)
- [Examples](examples/README.md)

## License

Apache 2.0
