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

Claude Code and other Anthropic clients — no code change:

```bash
export ANTHROPIC_BASE_URL=http://localhost:8080
export ANTHROPIC_AUTH_TOKEN=<MUTEGATE_API_KEY>   # your Mutegate key, not the Anthropic one
claude
```

OpenAI SDKs and OpenAI-compatible tools:

```bash
export OPENAI_BASE_URL=http://localhost:8080/v1
export OPENAI_API_KEY=<MUTEGATE_API_KEY>
```

Setup for Codex, Cursor, Cline, Continue, Aider, OpenCode, the SDKs and
LangChain — and what each of them can and cannot do through the gateway — is in
[docs/CONNECT.md](docs/CONNECT.md). Every installation serves the same guides
at `/connect`, and the console shows them with your key filled in.
Runnable scripts are in [`examples/`](examples).

## With PostgreSQL and the console

```bash
docker compose up -d    # postgres + gateway (:8080) + console (http://localhost:5173)
```

Open http://localhost:5173 and sign up: you get an organization of your own.
Add a provider key under **Settings → Providers**, create a key for your agent
under **Settings → API keys**, and point the agent at `http://localhost:8080`.
Invite colleagues from **Settings → Members**; limits and alerts are in
Settings too, so the whole setup is doable without curl.

This stack listens on `127.0.0.1` only, which is why sign-up is open. On a
server, use `docker-compose.prod.yml`, where it is closed and the first
account comes by invitation — see [DEPLOY.md](docs/DEPLOY.md).

![Overview — traffic, spend and DLP at a glance](docs/screenshots/overview.png)

Without a database the console still works: paste the admin key under
**Settings → Console access**. HTTPS and sign-in with Google, GitHub or
Yandex are in [DEPLOY.md](docs/DEPLOY.md) too.

## Documentation

| | |
|---|---|
| [Connect your tools](docs/CONNECT.md) | Claude Code, Codex, Cursor, Cline, Continue, Aider, OpenCode, the SDKs, LangChain |
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
