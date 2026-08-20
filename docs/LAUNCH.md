# Launch drafts

Черновики постов для запуска (актуальны для v0.2.0). Перед публикацией:
проверить, что квикстарт из README проходит на чистой машине, приложить
скриншоты из `docs/screenshots/`.

Цифры в постах воспроизводимы: скан — `go test ./internal/inspector/ -bench
ScanChatRequest` (0.25 мс на теле 1637 Б), латентность NER и окна по SSE —
замеры из `README.md` и `ner/README.md`, память — `docker stats` на живом
деплое (3.1 МБ).

---

## Show HN (news.ycombinator.com)

**Title:**
Show HN: Aperture – self-hosted DLP gateway that stops AI agents from leaking secrets

**Text:**

Hi HN. I built Aperture after watching coding agents casually paste AWS keys
and customer emails into LLM prompts.

Agents make the old data-leak problem worse in a specific way: they read your
.env files, your logs, your database rows — and then they talk to a cloud API.
The leak usually isn't in the message a human typed; it's in the tool result
the agent quietly attached.

Aperture is a Go binary that sits between your agents and LLM providers as an
OpenAI-compatible proxy. Everything is scanned before it leaves your network:

- secrets (AWS/GitHub/GitLab/Slack tokens, private keys, JWTs) → blocked
- PII (emails, cards w/ Luhn, phones, IBANs w/ mod-97) → redacted in place
- your own regex stop-words → alert/redact/block, per key, hot-reloaded
- the whole request, not just the visible message: system prompt, multimodal
  text, tool-call arguments and tool results

Since it's a proxy, it has to speak what agents actually speak: OpenAI chat
completions, the OpenAI Responses API (Codex), and the native Anthropic
Messages API — so Claude Code is covered by setting one env var. Any
OpenAI-compatible endpoint (DeepSeek, Qwen, a local Ollama/vLLM) can be routed
by model prefix.

Two things are opt-in because they cost latency, and I'd rather you turn them
on deliberately:

- **Response scanning.** A model can echo a secret back, and the agent then
  carries it somewhere else. Streaming is the hard part: the answer is scanned
  through a 256-byte sliding window, so a key split across three SSE chunks is
  still caught — redacted in flight, or the stream is torn down with an in-band
  error. Costs a fixed lag before the first token; the answer finishes at the
  same time.
- **Names and addresses.** Regexes can't find "Ivan Petrov" or "7 Tverskaya
  St". A local NER model (EN+RU) runs as a sidecar next to the gateway — never
  a cloud API; the gateway refuses a NER_URL that isn't loopback or private.
  Adds ~30ms to a short prompt.

Operationally: per-key budgets and rate limits (a looping agent gets 429, not
your monthly spend), Prometheus /metrics, agent/session attribution, and an
audit report that answers "what would have been blocked if we enabled block" —
because nobody flips a DLP gateway to block-everything on day one.

The regex path costs ~0.25ms on a 1.6KB request. Incidents are stored with
masked samples only; raw sensitive content is never persisted. False positives
have escape hatches (allowlist, per-key rule muting) that stay visible instead
of silently hiding traffic.

What it deliberately doesn't do: browser traffic to ChatGPT/Claude web (that's
CASB territory), de-redaction (placeholders don't come back in the answer), and
NER over streamed responses (a model call per chunk blows any latency budget).
Rate/budget counters are per-instance, so behind a load balancer each instance
enforces its own share.

Stack: Go stdlib + pgx, React console, Apache 2.0. Runs with or without
Postgres (in-memory mode for trying it out).

Repo: https://github.com/danilovid/aperture
Quickstart is 2 minutes: docker run, curl a fake AWS key, watch it get 403'd.

Would love feedback — especially from anyone running agent fleets in prod:
what detectors or policies are missing before you'd put this in front of your
traffic?

---

## Reddit r/selfhosted

**Title:**
Aperture — self-hosted DLP gateway for AI agents (one Go binary, ~3MB RAM, Apache 2.0)

**Text:**

If your team uses coding agents or LLM APIs, everything they send goes to a
third-party cloud — including whatever secrets and PII end up in prompts. And
agents attach a lot you never typed: file contents, logs, tool output.

Aperture is an OpenAI-compatible proxy you run in your own network. It scans
outbound requests (AWS keys, tokens, private keys, emails, cards, custom
regexes), blocks or redacts them, logs incidents with masked samples, and pings
Slack/Telegram on blocks. Point any OpenAI SDK or agent at it by changing
base_url.

- one binary, no deps; optional Postgres for persistence
- speaks OpenAI chat + Responses API and the native Anthropic Messages API —
  Claude Code is covered by one env var; local Ollama/vLLM routes by prefix
- scans tool-call arguments and tool results, not just the visible message
- optional: response scanning (streaming included, sliding window across SSE
  chunks) and a local NER model for names/addresses — EN+RU, in a sidecar, no
  cloud
- per-key budgets and rate limits, Prometheus /metrics, incident feed, audit
  report ("what would have been blocked")
- provider keys AES-256-GCM encrypted at rest, gateway keys stored hashed
- web console: incident feed, per-key policies with live dry-run, cost tracking
- ~0.25ms scan overhead on a 1.6KB request; the gateway container sits at
  ~3MB RAM on my own box, next to Postgres and an unrelated shop

Repo: https://github.com/danilovid/aperture

---

## Reddit r/devops — короткая версия

**Title:**
We put a DLP proxy in front of our AI agents — open-sourced it

**Text:**

One-line integration (base_url), scans every LLM-bound request — including
tool-call arguments and tool results — for secrets and PII before it leaves the
network. Blocks or redacts, incident feed with masked samples, Slack alerts
with debounce, per-key budgets and rate limits, Prometheus metrics.

Optional stages: response scanning (streaming included, via a sliding window
over SSE chunks) and a local NER model for names/addresses.

Before you flip anything to "block": there's a report that tells you what
*would* have been blocked over the last week, per rule and per key.

Go, Apache 2.0, self-hosted. Repo: https://github.com/danilovid/aperture

Feedback welcome — what would you need before trusting it in prod?

---

## Чеклист перед публикацией

- [x] Репозиторий публичный, README-квикстарт проверен на чистой машине
- [x] CI зелёный на main; релиз `v0.2.0` с бинарниками (linux/darwin × amd64/arm64)
- [x] Образ `ghcr.io/danilovid/aperture:latest` опубликован (multi-arch, анонимный pull проверен)
- [x] Скриншоты в README отображаются на GitHub
- [x] GitHub topics проставлены
- [ ] Завести 3–5 issues из бэклога роадмапа, часть с меткой «good first issue» —
      сейчас открытых задач нет, и зашедшему разработчику не за что взяться
- [ ] Обновить скриншоты консоли: на них нет вкладки Report и тумблеров
      Scan responses / NER
- [ ] Отправить посты — вторник–четверг, 15:00–17:00 UTC (пик HN)
