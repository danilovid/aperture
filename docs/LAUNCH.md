# Launch drafts

Черновики постов для запуска. Перед публикацией: заменить `<REPO_URL>`,
проверить, что квикстарт из README проходит на чистой машине, приложить
скриншоты из `docs/screenshots/`.

---

## Show HN (news.ycombinator.com)

**Title:**
Show HN: Aperture – self-hosted DLP gateway that stops AI agents from leaking secrets

**Text:**

Hi HN. I built Aperture after watching coding agents casually paste AWS keys
and customer emails into LLM prompts.

The average employee now sends sensitive data to an AI tool every few days,
and agents make it worse: they read your .env files, your logs, your
databases — and then they talk to the cloud.

Aperture is a single Go binary that sits between your agents and LLM
providers (OpenAI/Anthropic/Groq) as an OpenAI-compatible proxy. Every
request is scanned before it leaves your network:

- secrets (AWS/GitHub/GitLab/Slack tokens, private keys, JWTs) → blocked
- PII (emails, cards w/ Luhn, phones, IBANs w/ mod-97) → redacted in place
- your own regex stop-words → alert/redact/block, per key, hot-reloaded

Everything lands in an incident feed with masked samples — the raw content
is never stored. Webhook alerts to Slack/Telegram with per key+rule
debounce, so a looping agent produces one alert, not three hundred.

Integration is one line — change base_url. Typical scan overhead is ~0.4ms.

What it deliberately doesn't do: browser traffic to ChatGPT web (that's
CASB territory), response scanning (yet), ML-based NER (regexes cover the
catastrophic leaks; NER is on the roadmap).

Stack: Go stdlib + pgx, React console, Apache 2.0. Runs with or without
Postgres (in-memory mode for trying it out).

Repo: <REPO_URL>
Quickstart is 2 minutes: docker run, curl a fake AWS key, watch it get 403'd.

Would love feedback — especially from anyone running agent fleets in prod:
what detectors/policies are missing before you'd put this in front of your
traffic?

---

## Reddit r/selfhosted

**Title:**
Aperture — self-hosted DLP gateway for AI agents (single Go binary, Apache 2.0)

**Text:**

If your team uses coding agents / LLM APIs, everything they send goes to a
third-party cloud — including whatever secrets and PII end up in prompts.

Aperture is an OpenAI-compatible proxy you run in your own network. It scans
outbound requests (AWS keys, tokens, private keys, emails, cards, custom
regexes), blocks or redacts them, logs incidents with masked samples, and
pings Slack/Telegram on blocks. Point any OpenAI SDK/agent at it by changing
base_url.

- single binary, no deps; optional Postgres for persistence
- provider keys AES-256-GCM encrypted at rest, gateway keys stored hashed
- web console: incident feed, per-key policies with live dry-run, cost tracking
- ~0.4ms scan overhead

Repo: <REPO_URL>

---

## Reddit r/devops — короткая версия

**Title:**
We put a DLP proxy in front of our AI agents — open-sourced it

**Text:**

One-line integration (base_url), scans every LLM-bound request for
secrets/PII before it leaves the network, blocks or redacts, incident feed
with masked samples, Slack alerts with debounce. Go, Apache 2.0, self-hosted.
Repo: <REPO_URL>. Feedback welcome — what would you need before trusting it
in prod?

---

## Чеклист перед публикацией

- [ ] Репозиторий публичный, README-квикстарт проверен на чистой машине
- [ ] `<REPO_URL>` заменён во всех драфтах
- [ ] CI зелёный на main; релиз-тег v0.1.0
- [ ] Скриншоты в README отображаются на GitHub
- [ ] GitHub topics: `dlp`, `ai-agents`, `llm-gateway`, `security`, `golang`, `self-hosted`
- [ ] Issues включены; 3–5 «good first issue» из backlog (NER-детекторы, response scanning, провайдеры)
- [ ] Время поста: вторник–четверг, 15:00–17:00 UTC (пик HN)
