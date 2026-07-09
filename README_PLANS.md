# Aperture — Development Plan

## Project Overview

**Aperture** is an open-source LLM observability and proxy platform, similar to Helicone. It sits between applications and LLM providers (OpenAI, Anthropic, Groq), providing a unified API, token tracking, cost monitoring, and usage analytics.

## Tech Stack

- **Backend**: Go 1.22+ (stdlib `net/http` with method routing)
- **Frontend**: React + TypeScript (Vite)
- **Database**: PostgreSQL (pgx/v5), with in-memory fallback for dev
- **Auth**: Admin API key (current), OAuth 2.0 (planned)
- **API**: REST, OpenAI-compatible proxy

## Current Architecture

```
cmd/aperture/           # Entrypoint, wiring
internal/
  config/               # Env-based config, in-memory RuntimeStore
  interceptor/          # Wraps Provider to extract usage and log requests
  pricing/              # Per-model cost calculation (prefix matching)
  provider/             # Provider interface
    openai/             # OpenAI client
    anthropic/          # Anthropic client (OpenAI format translation)
    groq/               # Groq client
  server/               # HTTP handlers, routes, middleware, provider routing
  storage/              # KeyStore / LogStore interfaces, domain types
    postgres/           # PostgreSQL implementations, shared pool
web/                    # React frontend (chat + dashboard)
```

This structure uses **interface-based dependency inversion** without unnecessary layering. Interfaces live in `storage/` and `provider/`; implementations live in sub-packages. No `usecase` layer — handlers call storage directly, which is appropriate for the current complexity.

## What's Done

| Feature | Package | Status |
|---------|---------|--------|
| Proxy gateway (OpenAI-compatible) | `server`, `provider/*` | Done |
| SSE streaming with flush | `server/handlers.go` | Done |
| Anthropic -> OpenAI format translation | `provider/anthropic` | Done |
| Multi-provider routing by model name | `server/provider_router.go` | Done |
| Token usage interception | `interceptor` | Done |
| Cost calculation (per-model pricing table) | `pricing` | Done |
| PostgreSQL key + log storage | `storage/postgres` | Done |
| In-memory fallback (no-DB mode) | `config/runtime.go` | Done |
| Admin API (key config, CRUD) | `server/handlers.go` | Done |
| Stats API (summary, timeseries, models, logs) | `server/handlers.go` | Done |
| Admin auth (Bearer token) | `server/handlers.go` | Done |
| React chat UI | `web/src/App.tsx` | Done |
| React dashboard | `web/src/Dashboard.tsx` | Done |
| Docker Compose (app + PostgreSQL) | `docker-compose.yml` | Done |
| Graceful shutdown | `cmd/aperture/main.go` | Done |

---

## Issues / Tasks

### Phase 1: Stabilize

#### #1 — Fix OpenAI client double body read
- **Type**: Bug
- **Branch**: `fix/openai-body-read`
- **Files**: `internal/provider/openai/client.go`
- **Problem**: `doWithStatus` passes `body` as `reqBody` to `http.NewRequestWithContext`, then reads it again with `io.ReadAll(body)` on line 65. The second read gets an empty reader because the body was already consumed.
- **Fix**: Remove the redundant `io.ReadAll` block. Read the body once, create the request from the buffer.
- **Tests**: Add `provider/openai/client_test.go` — round-trip test with `httptest.Server` verifying the request body arrives intact.

#### #2 — Add SQL migrations with goose
- **Type**: Infrastructure
- **Branch**: `feat/sql-migrations`
- **Files**: `migrations/001_initial.sql` (new), `internal/storage/postgres/keystore.go`, `internal/storage/postgres/logstore.go`, `cmd/aperture/main.go`
- **What**:
  - Extract the `CREATE TABLE` / `CREATE INDEX` statements from `keystore.go` and `logstore.go` into `migrations/001_initial.sql`.
  - Add `pressly/goose` dependency.
  - Run migrations on startup (before creating stores).
  - Remove inline schema execution from `NewKeyStore` and `NewLogStore`.
- **Acceptance**: `goose up` applies schema from scratch; `goose status` shows applied migrations; app boots normally.

#### #3 — Tests: pricing
- **Type**: Tests
- **Branch**: `test/pricing`
- **Files**: `internal/pricing/pricing_test.go` (new)
- **What**: Table-driven tests for `Calculate`:
  - Exact model match (`gpt-4o-mini`)
  - Prefix match (`gpt-4o-mini-2024-07-18` -> `gpt-4o-mini`)
  - Unknown model returns 0
  - Zero tokens returns 0
  - All providers (OpenAI, Anthropic, Groq models)

#### #4 — Tests: provider routing
- **Type**: Tests
- **Branch**: `test/provider-routing`
- **Files**: `internal/server/provider_router_test.go` (new)
- **What**: Table-driven tests for `modelToLLM`:
  - `gpt-4o` -> `openai`
  - `claude-3-5-sonnet-20241022` -> `anthropic`
  - `llama-3.3-70b-versatile` -> `groq`
  - `mixtral-8x7b-32768` -> `groq`
  - Unknown model -> `openai` (default)
  - Case insensitivity (`Claude-3` -> `anthropic`)

#### #5 — Tests: interceptor
- **Type**: Tests
- **Branch**: `test/interceptor`
- **Files**: `internal/interceptor/interceptor_test.go` (new)
- **What**:
  - Mock `provider.Provider` and `storage.LogStore`.
  - Test non-streaming: verify `LogStore.Insert` is called with correct tokens/cost/latency.
  - Test streaming: feed SSE lines with a usage chunk, verify it's captured.
  - Test error case: provider returns error, verify log entry has error string.
  - Test nil LogStore: no panic, no insert.

#### #6 — Tests: HTTP handlers
- **Type**: Tests
- **Branch**: `test/handlers`
- **Files**: `internal/server/handlers_test.go` (new)
- **What**: Use `httptest.NewServer` with mock `KeyStore` / `LogStore`:
  - `GET /health` -> 200
  - `GET /ready` -> 200
  - `GET /admin/config` without admin key -> 401
  - `GET /admin/config` with admin key -> 200 + JSON
  - `POST /admin/config` saves keys
  - `DELETE /admin/config` clears keys
  - `GET /admin/stats/*` without LogStore -> 503
  - `POST /v1/chat/completions` without Bearer -> 401

#### #7 — Store request/response bodies
- **Type**: Feature
- **Branch**: `feat/request-body-logging`
- **Files**: `internal/storage/logstore.go`, `internal/storage/postgres/logstore.go`, `internal/interceptor/interceptor.go`, `migrations/002_add_request_body.sql` (new)
- **What**:
  - Add `request_body TEXT` and `response_body TEXT` columns to `request_logs`.
  - Update `LogEntry` struct with `RequestBody` and `ResponseBody` fields.
  - In interceptor: capture the request JSON and full response body (for non-streaming; for streaming, concatenate delta content).
  - Add a size cap (e.g. 64KB) to avoid storing huge payloads.
- **API change**: `GET /admin/stats/logs` response includes `request_body` and `response_body` fields.

#### #8 — Latency percentiles (p50/p90/p99)
- **Type**: Feature
- **Branch**: `feat/latency-percentiles`
- **Files**: `internal/storage/logstore.go`, `internal/storage/postgres/logstore.go`, `internal/server/handlers.go`, `web/src/Dashboard.tsx`
- **What**:
  - Add `P50LatencyMs`, `P90LatencyMs`, `P99LatencyMs` to `StatsSummary`.
  - Use PostgreSQL `percentile_cont` in the summary query.
  - Display percentiles on the dashboard.

---

### Phase 2: Multi-user & Auth

#### #9 — User model and migration
- **Type**: Feature
- **Branch**: `feat/user-model`
- **Files**: `migrations/003_users.sql` (new), `internal/storage/user.go` (new), `internal/storage/postgres/userstore.go` (new)
- **What**:
  - Create `users` table: `id UUID PK`, `email TEXT UNIQUE`, `name TEXT`, `avatar_url TEXT`, `provider TEXT`, `provider_id TEXT`, `created_at TIMESTAMPTZ`.
  - Define `UserStore` interface: `GetByID`, `GetByProviderID`, `Create`, `Update`.
  - Implement PostgreSQL `UserStore`.
- **No handler changes yet** — this is just the data layer.

#### #10 — GitHub OAuth
- **Type**: Feature
- **Branch**: `feat/github-oauth`
- **Files**: `internal/auth/` (new package), `internal/server/auth_handlers.go` (new), `internal/server/routes.go`, `cmd/aperture/main.go`, `internal/config/config.go`
- **What**:
  - Add `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`, `JWT_SECRET` to config.
  - Implement OAuth flow: `GET /auth/github` (redirect), `GET /auth/github/callback` (exchange code, upsert user, return JWT).
  - JWT middleware that extracts user from token and injects into context.
  - `GET /api/v1/me` returns current user profile.
- **Env vars**: `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`, `JWT_SECRET`.

#### #11 — Google OAuth
- **Type**: Feature
- **Branch**: `feat/google-oauth`
- **Files**: `internal/auth/google.go` (new), `internal/server/auth_handlers.go`, `internal/server/routes.go`, `internal/config/config.go`
- **What**: Same flow as GitHub but with Google's OAuth endpoints.
- **Depends on**: #10 (shared JWT middleware and auth package).

#### #12 — Link API keys to users
- **Type**: Feature
- **Branch**: `feat/user-api-keys`
- **Files**: `migrations/004_user_api_keys.sql` (new), `internal/storage/keystore.go`, `internal/storage/postgres/keystore.go`, `internal/server/handlers.go`
- **What**:
  - Add `user_id UUID FK` column to `api_keys` (nullable for backward compat).
  - New endpoints: `POST /api/v1/keys` (create key for authenticated user), `GET /api/v1/keys` (list own keys), `DELETE /api/v1/keys/{id}` (revoke own key).
  - Existing admin endpoints continue to work.
- **Depends on**: #9, #10.

#### #13 — Per-key usage tracking
- **Type**: Feature
- **Branch**: `feat/per-key-stats`
- **Files**: `internal/storage/logstore.go`, `internal/storage/postgres/logstore.go`, `internal/server/handlers.go`
- **What**:
  - Add `key_id` filter to `Summary`, `Timeseries`, `ModelStats` queries.
  - New endpoint: `GET /api/v1/keys/{id}/stats` — usage for a specific key.
  - Dashboard shows breakdown by key.
- **Depends on**: #12.

---

### Phase 3: Controls & Limits

#### #14 — Budget model and migration
- **Type**: Feature
- **Branch**: `feat/budget-model`
- **Files**: `migrations/005_budgets.sql` (new), `internal/storage/budget.go` (new), `internal/storage/postgres/budgetstore.go` (new)
- **What**:
  - Create `budgets` table: `id UUID PK`, `user_id UUID FK UNIQUE`, `monthly_limit_usd NUMERIC`, `alert_threshold_pct INT`, `created_at TIMESTAMPTZ`.
  - Define `BudgetStore` interface: `Get(userID)`, `Set(userID, limit, threshold)`.
  - Implement PostgreSQL `BudgetStore`.
  - Endpoints: `GET /api/v1/budget`, `PUT /api/v1/budget`.
- **Depends on**: #9.

#### #15 — Enforce budget hard stop
- **Type**: Feature
- **Branch**: `feat/budget-enforcement`
- **Files**: `internal/server/handlers.go`, `internal/server/provider_router.go`
- **What**:
  - Before proxying a request, check the user's current month spend against their budget limit.
  - If over limit, return `429 Too Many Requests` with `{"error": "monthly budget exceeded"}`.
  - Query: `SELECT SUM(cost_usd) FROM request_logs WHERE key_id = $1 AND ts >= date_trunc('month', NOW())`.
- **Depends on**: #14, #12.

#### #16 — Budget alerts (webhook)
- **Type**: Feature
- **Branch**: `feat/budget-alerts`
- **Files**: `internal/alert/` (new package), `internal/interceptor/interceptor.go`
- **What**:
  - After logging a request, check if cumulative spend crossed the alert threshold.
  - Fire a webhook (`POST` to a configured URL) with spend summary.
  - Add `alert_webhook_url TEXT` to `budgets` table.
- **Depends on**: #14, #15.

---

### Phase 4: UI Polish

#### #17 — Settings UI for all providers
- **Type**: Feature
- **Branch**: `feat/multi-provider-settings`
- **Files**: `web/src/App.tsx`
- **What**:
  - Replace single OpenAI key input with fields for OpenAI, Anthropic, and Groq.
  - Show which providers are configured (green indicator).
  - `POST /admin/config` already accepts all three keys — this is frontend-only.

#### #18 — Log filtering and search
- **Type**: Feature
- **Branch**: `feat/log-filters`
- **Files**: `web/src/Dashboard.tsx`, `internal/storage/logstore.go`, `internal/storage/postgres/logstore.go`, `internal/server/handlers.go`
- **What**:
  - Add query params to `GET /admin/stats/logs`: `model`, `provider`, `status`, `from`, `to`.
  - Update `LogFilter` struct and Postgres query.
  - Add filter controls to Dashboard UI (dropdowns, date pickers).

#### #19 — Per-request cost in chat
- **Type**: Feature
- **Branch**: `feat/chat-cost-display`
- **Files**: `web/src/App.tsx`
- **What**:
  - Parse usage from the final SSE chunk (already emitted by interceptor).
  - Display tokens and estimated cost below each assistant message.
  - Use the pricing table (embed a JS copy or fetch from a new endpoint).

#### #20 — Prompt templates
- **Type**: Feature
- **Branch**: `feat/prompt-templates`
- **Files**: `web/src/App.tsx` (or new component)
- **What**:
  - Save/load system prompts and message templates to localStorage.
  - Template picker in the chat sidebar.
  - Export/import as JSON.
- **No backend changes** — templates are client-side only (for now).

---

## Priority & Dependencies

```
Phase 1 (no dependencies between tasks):
  #1  Fix OpenAI body bug
  #2  SQL migrations
  #3  Tests: pricing
  #4  Tests: provider routing
  #5  Tests: interceptor
  #6  Tests: handlers
  #7  Request body logging         (depends on #2)
  #8  Latency percentiles          (depends on #2)

Phase 2 (sequential chain):
  #9  User model                   (depends on #2)
  #10 GitHub OAuth                 (depends on #9)
  #11 Google OAuth                 (depends on #10)
  #12 Link keys to users           (depends on #9, #10)
  #13 Per-key stats                (depends on #12)

Phase 3 (sequential chain):
  #14 Budget model                 (depends on #9)
  #15 Budget enforcement           (depends on #14, #12)
  #16 Budget alerts                (depends on #15)

Phase 4 (independent, can start anytime):
  #17 Multi-provider settings UI   (no deps)
  #18 Log filtering                (depends on #2)
  #19 Chat cost display            (no deps)
  #20 Prompt templates             (no deps)
```

## Go Conventions

- Go 1.22+ features: `http.NewServeMux` with method routing, `max`/`min` builtins, `slices`/`maps` packages
- `slog` for structured logging (stdlib)
- `errors.Is` / `errors.As` for error checking
- `context.Context` for all DB and HTTP calls
- `any` instead of `interface{}`
- Table-driven tests with `t.Run`
- Dependency injection via interfaces
- Consistent JSON error format: `{"error": "message"}`
- No ignored errors in production code

## Code Style

- No unnecessary abstraction layers — add complexity only when the code demands it
- Interfaces in the package that uses them, implementations in sub-packages
- Prefer stdlib over third-party when reasonable
- Keep handlers thin — extract shared logic into helpers, not "usecase" wrappers
- Migrations as plain SQL files in `/migrations`
- Secrets via environment variables, never hardcoded
