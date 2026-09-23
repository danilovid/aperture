# Aperture — authentication, roles and access control

The plan for authentication, authorization, roles and the machinery around
them.

> This document is about authenticating **machines** — the keys agents use.
> People, organizations and sessions are covered by
> [`MULTITENANCY.md`](MULTITENANCY.md).

---

## 1. Authentication

**The question:** how does a client prove it is who it claims to be?

### Options

| Method | Pros | Cons | When to use |
|--------|------|------|-------------|
| **API key** (Bearer) | Simple, familiar from LLM APIs | Key storage, rotation | MVP, most cases |
| **API key** (`X-API-Key` header) | An alternative to Bearer | The same | When clients need the compatibility |
| **JWT** | Stateless, claims, expiry | More complex, needs an issuer | Integrating with an existing IdP |
| **Basic auth** | Very simple | Weak for production | Development and tests only |
| **OAuth2 / OIDC** | Enterprise, SSO | Elaborate setup | Corporate customers |

### Recommendation by phase

| Phase | Implementation |
|-------|----------------|
| **MVP** | One API key from the environment — every request passes (or auth is off) |
| **v0.2** | Multiple API keys from configuration or a file, bound to the key |
| **v0.3** | JWT as an option (for Keycloak, Auth0 and the like) |
| **Later** | OAuth2/OIDC for enterprise |

### Request format (OpenAI-compatible)

```
Authorization: Bearer sk-aperture-xxx
# or
X-API-Key: sk-aperture-xxx
```

---

## 2. Authorization and roles

**The question:** what is allowed once the caller is authenticated?

### Roles (proposal)

| Role | Description | Rights |
|------|-------------|--------|
| **admin** | Administration | Everything, plus key and configuration management and metrics |
| **user** | An ordinary user | Calls `/v1/chat/completions` and `/v1/models` within their limits |
| **readonly** | Read only | `/v1/models`, possibly usage — without calling an LLM |
| **service** | A service account | Like user, but for machines and integrations, with its own limits |

### Permissions

Beyond roles, specific restrictions:

| Permission | Description |
|------------|-------------|
| `models:list` | Access to `/v1/models` |
| `chat:complete` | Calling chat completions |
| `embeddings:create` | Calling embeddings, if we add them |
| `models:allowed` | The list of permitted models (empty means all) |
| `providers:allowed` | The list of permitted providers |

### Bound to the key

```
key_id: sk-aperture-abc123
  role: user
  permissions: [models:list, chat:complete]
  allowed_models: [gpt-4o-mini, gpt-4o]   # empty = all
  rate_limit: 60/min
  budget_daily: 10.00   # USD
```

---

## 3. API keys

### Lifecycle

| Action | MVP | v0.2 | Later |
|--------|-----|------|-------|
| Creation | By hand in the configuration | Configuration or file | Admin API |
| Storage | Plaintext in the environment | File, environment | Hashed in the database (optional) |
| Rotation | Manual | Manual | Admin API plus TTL |
| Revocation | Remove from the configuration | Remove | Revoke through the API |

### Key format, so they can be told apart

```
sk-aperture-<random>   — an Aperture key (ours)
sk-proj-xxx            — may be passed upstream as-is (passthrough)
```

---

## 4. Multi-tenancy

**The question:** isolating data between customers (organizations, projects).

| Level | Description | Difficulty |
|-------|-------------|------------|
| **None** | Every key in one space | Simple |
| **Soft** | Keys tagged with a `tenant_id`, reporting per tenant | Moderate |
| **Hard** | Full isolation, separate limits and budgets per tenant | Harder |

**Recommendation:** soft at the start (`tenant_id` as an attribute of the key);
hard on explicit demand.

> Superseded: the project went with hard isolation. See
> [`MULTITENANCY.md`](MULTITENANCY.md).

---

## 5. The middleware pipeline

```
Request
   │
   ▼
┌──────────────┐
│ 1. Logging   │  — record the incoming request
└──────┬───────┘
       ▼
┌──────────────┐
│ 2. Auth      │  — take the key, check it, put it in the context
└──────┬───────┘
       ▼
┌──────────────┐
│ 3. Authorize │  — check the role and permissions for this endpoint
└──────┬───────┘
       ▼
┌──────────────┐
│ 4. Rate limit│  — check the limits for this key
└──────┬───────┘
       ▼
┌──────────────┐
│ 5. Budget    │  — check the daily or monthly ceiling, if any
└──────┬───────┘
       ▼
┌──────────────┐
│ 6. Router    │  — pick a provider, send the request
└──────┬───────┘
       ▼
┌──────────────┐
│ 7. Cost track│  — account for tokens and spend
└──────┬───────┘
       ▼
Response
```

---

## 6. Audit and logging

What to log for security and debugging:

| Event | Data |
|-------|------|
| A successful request | key_id (masked), model, tokens, timestamp |
| An auth failure | the reason (invalid key, expired) |
| A rate-limit refusal | key_id, the limit |
| A budget refusal | key_id, usage, the limit |
| An admin action | who, what, when (if there is an admin API) |

**Important:** never log request bodies — chat messages are personal data.

---

## 7. The revised development plan

### Phase 1 (MVP) — no auth
- Auth is off, or a single static key from the environment
- The goal is to prove the proxy

### Phase 2 — basic auth
- Multiple API keys from configuration
- The key is checked in middleware
- Requests are tied to a key_id for rate limiting and cost tracking

### Phase 3 — roles and permissions
- Roles: admin, user, readonly
- Permissions checked per endpoint
- allowed_models, allowed_providers

### Phase 4 — key management
- Admin API: create and revoke keys (optional)
- Or keep a configuration file, for simplicity

### Phase 5 (optional)
- JWT
- Multi-tenancy (`tenant_id`)
- OAuth2/OIDC

---

## 8. Configuration (example)

```yaml
auth:
  enabled: true
  keys:
    - id: sk-aperture-dev123
      name: "Development"
      role: admin
      # permissions: [models:list, chat:complete, admin]
    - id: sk-aperture-user456
      name: "App Backend"
      role: user
      allowed_models: [gpt-4o-mini]
      rate_limit: 100/min
      budget_daily: 5.00
```

---

## 9. Open questions

- [ ] Store keys hashed (bcrypt) or in plaintext, for simplicity?
- [ ] Build the admin API in, or keep a separate tool (CLI)?
- [ ] Is integrating with an external IdP needed in the first version?
