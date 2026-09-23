# Mutegate

**Self-hosted DLP gateway for AI agents.** A drop-in proxy between your agents
and LLM providers (OpenAI, Anthropic, Groq, any OpenAI-compatible endpoint)
that scans every request for secrets, PII and your own stop-patterns —
**before it leaves your network**.

Your agents talk to the cloud. Know what they say.

![Incidents — the feed of what was caught](docs/screenshots/incidents.png)

- **Block or redact** API keys and tokens, private keys, JWTs, emails, cards, phones, IBANs — plus your own regex rules, and names and addresses with an optional local NER model
- **Scans the whole request** — system prompt, tool-call arguments and tool results, not just the visible message — and, if you turn it on, the response, mid-stream
- **Incident feed** with masked samples only; raw sensitive content is never stored
- **Per-key policies** with a live dry-run, and a report of what `block` would have stopped before you turn it on
- **Budgets and rate limits** per key — a looping agent gets `429`, not your monthly spend
- **Cost and token tracking** per model, key and agent; webhook alerts; Prometheus metrics
- **Teams**: organizations, roles, invitations, service tokens and an audit log
- Speaks the OpenAI Chat Completions and Responses APIs and the native Anthropic Messages API — one Go binary, point your agent at it by changing `base_url`

```
 agents / apps ──► Mutegate (scan · block · redact · log) ──► OpenAI / Anthropic / Groq / …
```

## Quickstart

```bash
git clone https://github.com/danilovid/mutegate.git && cd mutegate
docker build -t mutegate .
docker run -p 8080:8080 -e OPENAI_API_KEY=sk-... mutegate
# The log prints a generated MUTEGATE_API_KEY and ADMIN_API_KEY.

curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer <MUTEGATE_API_KEY>" -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"deploy with AKIAIOSFODNN7EXAMPLE"}]}'
# → 403 {"error":{"type":"mutegate_dlp_blocked","rules":["aws-access-key"],...}}

curl -H "Authorization: Bearer <ADMIN_API_KEY>" http://localhost:8080/admin/dlp/events
# → the incident, with a masked sample: "AKIA****************"
```

Clean traffic passes through untouched, streaming included; PII is redacted in
place — the provider receives `[REDACTED:email]` instead of the address. This
mode keeps everything in memory; for keys, incidents and accounts that
survive a restart, [add PostgreSQL](#with-postgresql-and-the-console).

## Connecting an agent

Claude Code and other Anthropic clients — one variable, no code change:

```bash
export ANTHROPIC_BASE_URL=http://localhost:8080
export ANTHROPIC_API_KEY=<MUTEGATE_API_KEY>   # your Mutegate key, not the Anthropic one
claude
```

OpenAI SDKs and OpenAI-compatible tools:

```bash
export OPENAI_BASE_URL=http://localhost:8080/v1
export OPENAI_API_KEY=<MUTEGATE_API_KEY>
```

Send `X-Mutegate-Agent` and `X-Mutegate-Session` to tell agents sharing a key
apart in the feed and the cost figures. More in [`examples/`](examples) —
curl, the Python and Node SDKs, the Jev decision API, demo data.

## With PostgreSQL and the console

```bash
docker compose up -d    # postgres + gateway (:8080) + console (http://localhost:5173)
```

With a database the console is signed into with an account. Create the first
organization and its owner with the operator's key (`ADMIN_API_KEY`, printed
in `docker compose logs mutegate`):

```bash
curl -X POST http://localhost:8080/api/instance/organizations \
  -H "Authorization: Bearer $ADMIN_API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"Acme","owner_email":"you@company.com"}'
# → {"invitation":{"token":"…", ...}, ...}
```

Open `http://localhost:5173/invite/<token>`, choose a password, and you are
in. Everybody else is invited from **Settings → Members**, or set
`REGISTRATION_OPEN=true` on the gateway to let people sign up. Keys,
providers, limits and alerts are all in **Settings**, so the whole setup is
doable without curl.

![Overview — traffic, spend and DLP at a glance](docs/screenshots/overview.png)

Without a database the console still works: paste the admin key under
**Settings → Console access**. Production setup, HTTPS and sign-in with
Google, GitHub or Yandex are in [DEPLOY.md](docs/DEPLOY.md).

## Documentation

| | |
|---|---|
| [DLP](docs/DLP.md) | Detectors, policies, false positives, response scanning, names and addresses, the rollout report |
| [Configuration](docs/CONFIGURATION.md) | Every environment variable |
| [Providers](docs/PROVIDERS.md) | Routing by model, custom OpenAI-compatible endpoints, proxies, the Jev decision API |
| [API](docs/API.md) | Endpoints, who may call them, attribution, budgets, metrics |
| [Deploy](docs/DEPLOY.md) | Docker Compose on a VPS, continuous deployment, first sign-in, OAuth |
| [Accounts and organizations](docs/MULTITENANCY.md) | Roles, sessions, service tokens, isolation between tenants |
| [Architecture](docs/ARCHITECTURE.md) · [Roadmap](docs/ROADMAP.md) | How it is built and where it is going |

## What Mutegate does not do

- It does not scan browser traffic to the ChatGPT or Claude web apps — it
  protects the **API path**: agents, SDKs, backends. For browser DLP look at
  enterprise CASB tooling.
- It does not scan responses unless a policy turns it on, and does not find
  names or addresses without the NER stage.

> **Formerly Aperture.** The old `APERTURE_*` environment variables and
> `X-Aperture-*` headers keep working alongside the new names.

## License

Apache 2.0
