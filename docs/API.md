# HTTP API

## Who can call what

**Agents** call the `/v1/*` and `/api/v1/*` routes with a **gateway key**
(`ap-…`) as `Authorization: Bearer`; the Anthropic route also takes it as
`x-api-key`, as Anthropic clients send it. With a database, keys are created
in **Settings → API keys** or with `POST /admin/keys`; without one there is a
single key, `MUTEGATE_API_KEY`.

The **administrative** routes, `/admin/*`, accept three kinds of caller:

| Caller | Credential | Acts in |
|--------|------------|---------|
| A person in the console | Session cookie; writes also carry `X-Mutegate-CSRF` | The organization on the session, within their role |
| CI and scripts | A service token `apt_…` from **Settings → Access tokens**, with scopes `events:read`, `keys:read`, `keys:write`, `policies:write` | The organization that issued it |
| The operator | `ADMIN_API_KEY` | The default organization, or the one named with `X-Mutegate-Org`. Also the only caller of `/api/instance/*` |

Without a database there are no accounts, and `/admin/*` takes the operator's
key only. Roles and isolation are described in
[MULTITENANCY.md](MULTITENANCY.md).

## Agent routes

| Route | What |
|-------|------|
| `POST /v1/chat/completions` | OpenAI-compatible chat, streaming included |
| `POST /v1/responses` | OpenAI Responses API |
| `POST /v1/messages` | Native Anthropic Messages API — what Claude Code speaks |
| `POST /api/v1/decisions`, `POST /api/v1/decisions/{preset}` | The [Jev decision API](PROVIDERS.md#the-jev-decision-api) |
| `GET /v1/models` | Models this key can use, asked live of every provider it has a credential for |

All of them are scanned by DLP before anything leaves the gateway.

**Attribution.** Several agents usually share one key, so send
`X-Mutegate-Agent` and `X-Mutegate-Session` to tell them apart. Both are
optional and land on incidents and usage rows, so the feed and the cost
figures can be split per agent or per run:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $MUTEGATE_API_KEY" \
  -H "X-Mutegate-Agent: ci-bot" -H "X-Mutegate-Session: build-4821" \
  -H "Content-Type: application/json" -d '{...}'
```

## Administration

| Route | What |
|-------|------|
| `GET /admin/dlp/events` | Incident feed; filters `action`, `rule`, `key_id`, `agent`, `session`, `limit`, `period` |
| `GET /admin/dlp/summary` | Blocked / redacted / alerted / suppressed counters for a period |
| `GET /admin/dlp/report` | What enabling `block` would have stopped (`period=24h\|7d\|30d`) |
| `GET /admin/policies`, `PUT /admin/policies/default`, `PUT\|DELETE /admin/policies/keys/{id}` | Default and per-key policies, applied immediately |
| `POST /admin/policies/test` | Dry-run: what a policy would do to a text |
| `POST /admin/policies/keys/{id}/mute`, `…/unmute` | Silence one detector for one key |
| `GET /admin/limits`, `PUT /admin/limits/default`, `PUT\|DELETE /admin/limits/keys/{id}` | Budgets and rate limits, plus today's spend |
| `GET\|PUT /admin/alerts`, `POST /admin/alerts/test` | The organization's alert webhook (the address is masked on read) |
| `GET\|POST /admin/keys`, `DELETE /admin/keys/{id}` | Gateway keys; the key itself is returned once, at creation |
| `GET /admin/providers`, `PUT\|DELETE /admin/providers/{name}`, `POST /admin/providers/test` | The organization's providers and a connectivity check |
| `GET\|POST\|DELETE /admin/config` | Provider keys of the default gateway key |
| `GET /admin/stats/summary`, `…/timeseries`, `…/models`, `…/logs` | Requests, tokens, cost and latency |
| `GET /admin/audit` | Who changed what: keys, policies, limits, providers, alerts, people, tokens (`group`, `before`, `limit`) |

## Accounts and organizations

These exist only with a database. They belong to people signed in to the
console — a service token cannot reach them — except `/api/instance/*`, which
is the operator's.

| Route | What |
|-------|------|
| `POST /api/auth/login`, `/logout`, `/signup`, `/register`; `GET /api/auth/me` | Sign-in, sign-up (with `REGISTRATION_OPEN`), accepting an invitation as a new account, the current session |
| `POST /api/auth/switch-org` | Switch the session to another organization of the same person |
| `GET /api/auth/providers`, `/api/auth/oauth/{provider}/…`, `/api/auth/identities` | Sign-in with Google, GitHub or Yandex, and the identities connected to an account |
| `GET\|POST /api/invitations`, `DELETE /api/invitations/{id}`, `POST /api/invitations/lookup`, `…/accept` | Invitations |
| `GET /api/members`, `PUT /api/members/{id}/role`, `DELETE /api/members/{id}` | Members and roles |
| `GET\|POST /api/tokens`, `DELETE /api/tokens/{id}` | Service tokens |
| `PATCH\|DELETE /api/organizations/current`, `POST …/leave` | Rename, close or leave the organization |
| `POST /api/instance/organizations`, `…/{id}/invitations`, `…/{id}/restore` | The operator: create an organization with its owner, invite the first owner into an existing one, restore a closed one |

## Budgets and rate limits

Give a key a daily spend ceiling and a request rate, so a looping agent cannot
burn the month's budget overnight. Over the limit the gateway answers `429`
with `Retry-After`, the upstream is never called, and the cut-off lands in the
incident feed and the alert webhook (once per key per day, not per rejected
request):

```bash
curl -X PUT http://localhost:8080/admin/limits/keys/<KEY_ID> \
  -H "Authorization: Bearer $ADMIN_API_KEY" -H "Content-Type: application/json" \
  -d '{"budget_daily_usd": 10, "requests_per_minute": 60}'
```

Budgets reset at 00:00 UTC, and today's spend is recovered from the request
log on restart, so a restart does not hand a key a fresh budget. Counters are
per instance: behind a load balancer each instance enforces its own share.

## Health and metrics

`GET /health` is liveness; `GET /ready` is readiness and pings PostgreSQL when
one is configured.

`GET /metrics` exposes the gateway in the Prometheus text format, so traffic,
spend and DLP activity land on the dashboards you already have:

```
mutegate_http_requests_total{path,status}          mutegate_tokens_total{direction}
mutegate_http_request_duration_seconds{le}         mutegate_cost_usd_total{provider}
mutegate_llm_requests_total{provider,model,status} mutegate_dlp_events_total{rule,action}
mutegate_limit_denied_total{reason}                mutegate_ner_latency_seconds
```

```yaml
scrape_configs:
  - job_name: mutegate
    static_configs: [{targets: ["localhost:8080"]}]
```

The endpoint needs no token and never exposes key material — labels are route
patterns (`/admin/keys/{id}`), never raw paths, so ids stay out of the label
set and the series count stays bounded. It is still an operational surface:
keep it on an internal network, or let your reverse proxy gate `/metrics`.
