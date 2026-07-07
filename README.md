# Aperture

Self-hosted DLP gateway for AI agents — a unified proxy between your applications/agents and LLM providers (OpenAI, Anthropic, Groq) that scans every request for secrets, PII and custom stop-patterns **before it leaves your network**.

- **Block or redact** AWS keys, GitHub/GitLab/Slack tokens, private keys, JWTs, emails, credit cards, phone numbers, IBANs
- **Incident feed**: who sent what, when — with masked samples (raw sensitive content is never stored)
- **Cost & token tracking** per model and key
- Single Go binary, OpenAI-compatible API: point your agent at it by changing `base_url`

## Getting started

**Option A: no database** — keys live in memory for the lifetime of the process:
```bash
export OPENAI_API_KEY=sk-...        # provider key, picked up on startup (optional)
export APERTURE_API_KEY=ap-my-key   # client Bearer token (optional)
export ADMIN_API_KEY=my-admin-key   # admin panel/API token (optional)
go run ./cmd/aperture
# Any key you don't set is generated at startup and printed in the log.
# Open http://localhost:5173 → ⚙ Settings → enter the aperture & admin keys
# (and provider keys, if not set via env).
```

Point your app at the gateway:
```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $APERTURE_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}'
```

See DLP in action:
```bash
# Blocked (secret detected) — never reaches the provider:
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $APERTURE_API_KEY" -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"deploy with AKIAIOSFODNN7EXAMPLE"}]}'
# → 403 {"error":{"type":"aperture_dlp_blocked","rules":["aws-access-key"],...}}

# Incident feed:
curl -H "Authorization: Bearer $ADMIN_API_KEY" http://localhost:8080/admin/dlp/events
```

**Option B: with PostgreSQL** — persistent keys and request stats:
```bash
docker compose up -d
export DATABASE_URL=postgres://aperture:aperture@localhost:5432/aperture?sslmode=disable
export ADMIN_API_KEY=your-admin-secret
go run ./cmd/aperture
```

## Environment variables

- `DATABASE_URL` — PostgreSQL connection string (if set, keys are stored in DB)
- `APERTURE_API_KEY` — Bearer token clients use to call the gateway (no-DB mode; generated and logged at startup if unset)
- `ADMIN_API_KEY` — Bearer token for all `/admin/*` endpoints (generated and logged at startup if unset; admin API is never open)
- `OPENAI_API_KEY` — OpenAI provider key, seeded on startup in no-DB mode (optional)
- `ANTHROPIC_API_KEY` — Anthropic (Claude) key, optional
- `GROQ_API_KEY` — Groq (Llama, Mixtral) key, optional
- `OPENAI_BASE_URL` — base URL (default: `https://api.openai.com`)
- `ALLOWED_ORIGINS` — comma-separated CORS allowlist for browser clients (default: `http://localhost:5173,http://localhost:4173`)
- `PORT` — listen port (default: `8080`)
- `DLP_ENABLED` — outbound content scanning (default: `true`)
- `DLP_SECRETS_ACTION` — action for detected secrets: `off|alert|redact|block` (default: `block`)
- `DLP_PII_ACTION` — action for detected PII (default: `redact`)
- `DLP_CUSTOM_ACTION` — action for custom rules (default: `alert`)

Provider is selected by model name: `claude*` → Anthropic, `llama*`/`mixtral*` → Groq, everything else → OpenAI.

## Endpoints

| Path | Description |
|------|-------------|
| `GET /health` | Health check |
| `GET /ready` | Readiness check |
| `GET /v1/models` | List models (Bearer: aperture_key) |
| `POST /v1/chat/completions` | Chat completions (Bearer: aperture_key) |
| `GET /admin/config` | Key status (Bearer: admin_key) |
| `POST /admin/config` | Set provider keys (Bearer: admin_key) |
| `DELETE /admin/config` | Clear provider keys (Bearer: admin_key) |
| `GET /admin/keys` | List aperture keys (Bearer: admin_key) |
| `POST /admin/keys` | Create a key (Bearer: admin_key; requires PostgreSQL) |
| `DELETE /admin/keys/{id}` | Delete a key (Bearer: admin_key) |
| `GET /admin/dlp/events` | DLP incident feed (Bearer: admin_key; filters: action, rule, key_id, limit, period) |
| `GET /admin/dlp/summary` | DLP counters for a period (Bearer: admin_key) |
| `GET /admin/stats/summary` | Usage summary (Bearer: admin_key; requires PostgreSQL) |
| `GET /admin/stats/timeseries` | Request timeseries (Bearer: admin_key; requires PostgreSQL) |
| `GET /admin/stats/models` | Per-model breakdown (Bearer: admin_key; requires PostgreSQL) |
| `GET /admin/stats/logs` | Recent request logs (Bearer: admin_key; requires PostgreSQL) |

## Documentation

- [Architecture](docs/ARCHITECTURE.md)
- [Auth and roles](docs/AUTH_AND_ACCESS.md)
- [Roadmap](docs/ROADMAP.md)
