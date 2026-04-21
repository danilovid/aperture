# Aperture

AI Gateway — a unified proxy between your applications and LLM providers (OpenAI, Anthropic, Groq).

## Getting started

**Option A: no database** — set the key via the Admin panel UI or env var:
```bash
go run ./cmd/aperture
# Open http://localhost:5173 → ⚙ Settings → enter your OpenAI key
# Or: export OPENAI_API_KEY=sk-... (picked up on startup)
```

**Option B: with PostgreSQL** — dynamic keys via API:
```bash
docker compose up -d
export DATABASE_URL=postgres://aperture:aperture@localhost:5432/aperture?sslmode=disable
export ADMIN_API_KEY=your-admin-secret
go run ./cmd/aperture
```

Create a key:
```bash
curl -X POST http://localhost:8080/admin/keys \
  -H "Authorization: Bearer your-admin-secret" \
  -H "Content-Type: application/json" \
  -d '{"aperture_key":"sk-aperture-xxx","openai_api_key":"sk-openai-...","name":"my-key"}'
```

## Environment variables

- `DATABASE_URL` — PostgreSQL connection string (if set, keys are stored in DB)
- `OPENAI_API_KEY` — fallback key when `DATABASE_URL` is not set
- `ANTHROPIC_API_KEY` — Anthropic (Claude) key, optional
- `GROQ_API_KEY` — Groq (Llama, Mixtral) key, optional
- `OPENAI_BASE_URL` — base URL (default: `https://api.openai.com`)
- `ADMIN_API_KEY` — required for Admin API when using PostgreSQL
- `PORT` — listen port (default: `8080`)

Provider is selected by model name: `claude*` → Anthropic, `llama*`/`mixtral*` → Groq, everything else → OpenAI.

## Endpoints

| Path | Description |
|------|-------------|
| `GET /health` | Health check |
| `GET /ready` | Readiness check |
| `GET /v1/models` | List models (Bearer: aperture_key) |
| `POST /v1/chat/completions` | Chat completions (Bearer: aperture_key) |
| `GET /admin/config` | Key status (configured: bool) |
| `POST /admin/config` | Set provider keys (Bearer: admin_key) |
| `DELETE /admin/config` | Clear provider keys (Bearer: admin_key) |
| `GET /admin/keys` | List aperture keys (Bearer: admin_key) |
| `DELETE /admin/keys/{id}` | Delete a key (Bearer: admin_key) |
| `GET /admin/stats/summary` | Usage summary (requires PostgreSQL) |
| `GET /admin/stats/timeseries` | Request timeseries (requires PostgreSQL) |
| `GET /admin/stats/models` | Per-model breakdown (requires PostgreSQL) |
| `GET /admin/stats/logs` | Recent request logs (requires PostgreSQL) |

## Documentation

- [Architecture](docs/ARCHITECTURE.md)
- [Auth and roles](docs/AUTH_AND_ACCESS.md)
