# Launch drafts

Launch post drafts (current as of v0.2.0). Before publishing: check that the
README quickstart works on a clean machine, and attach the screenshots from
`docs/screenshots/`.

Every number in the posts is reproducible: the scan from `go test
./internal/inspector/ -bench ScanChatRequest` (0.25 ms on a 1637-byte body),
the NER latency and the SSE window from the measurements in `README.md` and
`ner/README.md`, the memory figure from `docker stats` on the live deployment
(3.1 MB).

---

## Show HN (news.ycombinator.com)

**Title:**
Show HN: Mutegate – self-hosted DLP gateway that stops AI agents from leaking secrets

**Text:**

Hi HN. I built Mutegate after watching coding agents casually paste AWS keys
and customer emails into LLM prompts.

Agents make the old data-leak problem worse in a specific way: they read your
.env files, your logs, your database rows — and then they talk to a cloud API.
The leak usually isn't in the message a human typed; it's in the tool result
the agent quietly attached.

Mutegate is a Go binary that sits between your agents and LLM providers as an
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

Repo: https://github.com/Mutegate/mutegate
Quickstart is 2 minutes: docker run, curl a fake AWS key, watch it get 403'd.

Would love feedback — especially from anyone running agent fleets in prod:
what detectors or policies are missing before you'd put this in front of your
traffic?

---

## Reddit r/selfhosted

**Title:**
Mutegate — self-hosted DLP gateway for AI agents (one Go binary, ~3MB RAM, Apache 2.0)

**Text:**

If your team uses coding agents or LLM APIs, everything they send goes to a
third-party cloud — including whatever secrets and PII end up in prompts. And
agents attach a lot you never typed: file contents, logs, tool output.

Mutegate is an OpenAI-compatible proxy you run in your own network. It scans
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

Repo: https://github.com/Mutegate/mutegate

---

## Reddit r/devops — the short version

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

Go, Apache 2.0, self-hosted. Repo: https://github.com/Mutegate/mutegate

Feedback welcome — what would you need before trusting it in prod?

---

## Pre-publication checklist

- [x] The repository is public and the README quickstart has been checked on a clean machine
- [x] CI is green on main; release `v0.2.0` ships binaries (linux/darwin × amd64/arm64)
- [ ] The image `ghcr.io/mutegate/mutegate:latest` is published (multi-arch, anonymous pull verified)
- [x] The README screenshots render on GitHub
- [x] GitHub topics are set
- [ ] Open three to five issues from the roadmap backlog, some labelled "good
      first issue" — there are no open issues right now, so a developer who
      arrives has nothing to pick up
- [ ] Refresh the console screenshots: they predate the Report tab and the
      Scan responses / NER toggles
- [ ] Send the posts — Tuesday to Thursday, 15:00–17:00 UTC (the HN peak)
