# Aperture

**Self-hosted DLP gateway for AI agents.** A drop-in proxy between your
applications/agents and LLM providers (OpenAI, Anthropic, Groq) that scans
every request for secrets, PII and custom stop-patterns — **before it leaves
your network**.

Your agents talk to the cloud. Know what they say.

![DLP Events — incident feed](docs/screenshots/dlp-events.png)

- **Block or redact** AWS keys, GitHub/GitLab/Slack tokens, private keys, JWTs, emails, credit cards, phones, IBANs — plus your own regex rules
- **Incident feed**: who sent what, when — with masked samples (raw sensitive content is never stored)
- **Per-key policies** with hot reload and a dry-run API ("what would happen to this text")
- **Webhook alerts** to Slack/Telegram/anything, with storm debounce
- **Cost & token tracking** per model and key
- Single Go binary, OpenAI-compatible API: point your agent at it by changing `base_url`

```
 agents / apps ──► Aperture (scan · block · redact · log) ──► OpenAI / Anthropic / Groq
```

## Quickstart: first caught secret in 2 minutes

```bash
docker build -t aperture . && docker run -p 8080:8080 -e OPENAI_API_KEY=sk-... aperture
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

More: [`examples/`](examples) — curl, OpenAI Python/Node SDKs, pointing
coding agents at the gateway, demo seeding.

## Web console

```bash
cd web && npm ci && npm run dev   # http://localhost:5173
# ⚙ Settings → paste the admin & aperture keys from the server log
```

Overview (traffic + DLP KPIs), DLP Events (filterable incident feed),
Policies (per-key detector toggles with live dry-run preview), Settings & Keys.

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
| `DLP_WEBHOOK_URL` / `DLP_WEBHOOK_FORMAT` / `DLP_WEBHOOK_ACTIONS` / `DLP_WEBHOOK_CHAT_ID` | Alerts: `json`/`slack`/`telegram`, actions filter (default `blocked`) |
| `OPENAI_BASE_URL` | Override upstream (default `https://api.openai.com`) |
| `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` | Route upstream provider calls through a corporate egress proxy (standard Go proxy env vars) |
| `ALLOWED_ORIGINS` | CORS allowlist (default: localhost dev origins) |
| `PORT` | Listen port (default `8080`) |

Provider is selected by model name: `claude*` → Anthropic, `llama*`/`mixtral*` → Groq, everything else → OpenAI.

## API

| Path | Description |
|------|-------------|
| `POST /v1/chat/completions` | OpenAI-compatible chat (Bearer: aperture_key); scanned by DLP |
| `GET /v1/models` | List models (Bearer: aperture_key) |
| `GET /admin/dlp/events` | Incident feed; filters: action, rule, key_id, limit, period |
| `GET /admin/dlp/summary` | Blocked/redacted/alerted counters for a period |
| `GET/PUT /admin/policies…` | Default & per-key policies, hot-applied; `POST /admin/policies/test` dry-run |
| `GET/PUT /admin/alerts` | Webhook alert config (URL masked on read); `POST /admin/alerts/test` |
| `GET/POST/DELETE /admin/keys…` | Aperture key management (PostgreSQL) |
| `GET/POST/DELETE /admin/config` | Provider keys for the default key |
| `GET /admin/stats/…` | Requests/tokens/cost/latency (PostgreSQL) |
| `GET /health` · `GET /ready` | Liveness · readiness (pings PostgreSQL when configured) |

All `/admin/*` routes require `Authorization: Bearer <ADMIN_API_KEY>`.

A policy maps detector groups to actions, plus optional custom rules:
```json
{"secrets":"block","pii":"redact","custom":"alert",
 "custom_rules":[{"name":"project-x","pattern":"project-x"}]}
```

## What Aperture does not do

- Does not scan browser traffic to ChatGPT/Claude web UIs — it protects the
  **API path** (agents, SDKs, backends). For browser DLP look at enterprise
  CASB tooling.
- Does not store raw sensitive content anywhere — events keep a masked sample
  only.
- MVP scans **requests** (what leaves your network), not responses.

## Documentation

- [Architecture](docs/ARCHITECTURE.md)
- [Roadmap](docs/ROADMAP.md)
- [Auth and roles](docs/AUTH_AND_ACCESS.md)
- [Examples](examples/README.md)

## License

Apache 2.0
