# Mutegate — architecture and development plan

## The decision: a modular monolith

**Why not microservices, at the start:**
- Simpler to deploy and to contribute to as open source
- No network hop between "services" — critical for a gateway
- One binary means simpler CI, releases and Docker image

**Modularity:** clear packages behind interfaces. If load demands it, a part
can be moved into its own process without rewriting everything.

---

## Architecture

```
                    ┌─────────────────────────────────────────┐
                    │              Mutegate Gateway            │
                    │  ┌─────────────────────────────────────┐│
  Client ──────────►│  │  HTTP Server (OpenAI-compatible)    ││
                    │  └─────────────────┬───────────────────┘│
                    │                    │                     │
                    │  ┌─────────────────▼───────────────────┐│
                    │  │  Router / Middleware                ││
                    │  │  - Auth, rate limit, cost tracking  ││
                    │  └─────────────────┬───────────────────┘│
                    │                    │                     │
                    │  ┌─────────────────▼───────────────────┐│
                    │  │  Provider Abstraction               ││
                    │  │  - OpenAI  - Anthropic  - Groq      ││
                    │  └─────────────────┬───────────────────┘│
                    └───────────────────┼─────────────────────┘
                                        │
         ┌──────────────────────────────┼──────────────────────────────┐
         │                              │                              │
         ▼                              ▼                              ▼
    ┌─────────┐                  ┌───────────┐                  ┌─────────┐
    │ OpenAI  │                  │ Anthropic │                  │  Groq   │
    └─────────┘                  └───────────┘                  └─────────┘
```

### Package layout (Go)

```
mutegate/
├── cmd/
│   └── mutegate/          # main, the entry point
├── internal/
│   ├── server/            # HTTP, routes
│   ├── middleware/        # auth, rate limit, logging
│   ├── router/            # picking a provider by model or rules
│   ├── provider/          # the interface and its implementations
│   │   ├── openai/
│   │   ├── anthropic/
│   │   └── groq/
│   ├── telemetry/         # cost tracking, metrics
│   └── auth/              # keys, roles, permissions (see docs/AUTH_AND_ACCESS.md)
├── pkg/                   # public libraries, if any are needed
│   └── openai/            # OpenAI API types, for compatibility
├── config/                # configuration, environment
└── docs/
```

---

## Development plan

### Phase 1: MVP (a proxy to OpenAI)

| # | Task | Result |
|---|------|--------|
| 1.1 | Initialise the Go module and the folder structure | `go mod init`, a basic layout |
| 1.2 | An HTTP server with the OpenAI endpoints | `/v1/chat/completions`, `/v1/models` |
| 1.3 | The OpenAI provider | proxying requests to api.openai.com |
| 1.4 | Configuration (API key, base URL) | environment and flags, nothing hard-coded |
| 1.5 | Streaming | SSE for chat completions |

**Done when:** setting `OPENAI_API_KEY` is enough to proxy requests.

---

### Phase 2: More providers

| # | Task | Result |
|---|------|--------|
| 2.1 | The `Provider` interface | one contract for every provider |
| 2.2 | The Anthropic provider | with mapping into the OpenAI format |
| 2.3 | The Groq provider | likewise |
| 2.4 | Multi-provider configuration | keys and base URLs for each |

---

### Phase 3: Routing and fallback

| # | Task | Result |
|---|------|--------|
| 3.1 | Routing by `model` | model → provider (configuration or rules) |
| 3.2 | Fallback | on a provider error, try another |
| 3.3 | Rules in configuration | YAML/JSON: `gpt-4 → openai`, `claude → anthropic` |

---

### Phase 4: Resilience and billing

| # | Task | Result |
|---|------|--------|
| 4.1 | Rate limiting | per key, per model |
| 4.2 | Cost tracking | tokens and spend per provider |
| 4.3 | Metrics | Prometheus or OpenTelemetry |
| 4.4 | Logging | structured logs (slog) |

---

## Open-source orientation

- **Licence:** MIT or Apache 2.0 — maximum compatibility
- **Documentation:** README, examples in `examples/`, possibly `docs/`
- **Configuration:** environment plus a single YAML/JSON file — no elaborate
  orchestration
- **Dependencies:** as few external ones as possible, the standard library
  preferred
- **Docker:** one `Dockerfile` and a `docker-compose.yml` for local runs

---

## Auth, roles and access

The detailed plan: **[docs/AUTH_AND_ACCESS.md](AUTH_AND_ACCESS.md)** —
authentication, roles, keys, the middleware pipeline, auditing.

---

## Not in the first version

- A UI or dashboard
- Persistent storage (in-memory or logs for now)
- Multi-tenancy (can be added later through auth)
- Plugins and extensions
