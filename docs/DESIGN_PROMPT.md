# Prompt for generating the Mutegate design (DLP gateway)

> Copy the block below into Claude (claude.ai or Claude Code).
> Suggested model: Fable 5 for one pass, Sonnet 5 for quick iterations.

---

# The task

Design and build the interface for Mutegate — a self-hosted DLP gateway for AI
agents. We need an interactive React prototype: a landing page plus an admin
console. One self-contained component, no external dependencies and no network
requests — mock the data.

# What the product is

Mutegate is a proxy between a team's applications and AI agents and the LLM
providers (OpenAI, Anthropic, Groq). It is integrated by changing `base_url`.
The value is:

1. **DLP inspection**: every request is scanned before it reaches the cloud —
   secrets (AWS keys, tokens, private keys), PII (email addresses, cards,
   phone numbers), custom stop-words. Actions: block / redact / alert.
2. **An incident log**: who tried to send what, when, with a masked sample.
3. **Observability**: tokens, spend, latency and errors per model and per key.

Self-hosted, one Go binary, and the data never leaves the company's network.
The buyer is a CTO, tech lead or DevSecOps in a team running AI agents (Claude
Code and the like). Comparable in spirit: Helicone, Portkey, Nightfall — but
simpler and local.

# Visual direction

- Mood: a security tool people trust — precise, calm, instrument-like.
  Reference class: Linear, Vercel, Grafana, Tailscale.
- Dark theme first, light as an option; both deliberate.
- One accent colour (propose it) over a neutral base. Strict semantics:
  red = blocked, amber = redacted/alert, green = clean/allowed. Separate
  badges for providers (OpenAI/Anthropic/Groq).
- The name's metaphor is a Mutegate — a lens: everything passes through the
  focus.
- A monospace face for numbers, keys and code fragments; tabular figures.
- No stock illustrations; sparklines, status badges, dense tables.

# Screen 1 — Landing

Hero: "Your agents talk to the cloud. Know what they say." (or propose better)
plus a subheading about the self-hosted DLP gateway and two CTAs ("Get started"
/ "GitHub"). Sections: how it works (a diagram: agents → Mutegate (scan) →
providers), an integration example (a code snippet replacing `base_url`), three
features (DLP inspection, incident log, cost tracking), a "your data never
leaves your network" block, and a footer.

# Screen 2 — Dashboard / Overview (the main one)

A period selector (24h / 7d / 30d). KPI cards: Requests, DLP events (split into
blocked/redacted), Total tokens, Cost USD, Avg latency, Error rate. Each card
carries a sparkline and a delta against the previous period. Below: a timeseries
chart (switching between requests / DLP events / cost / latency) and a "By
model" table (model, provider badge, requests, tokens, cost, avg latency).

# Screen 3 — DLP Events (the one that matters most)

The incident feed: time, key or agent, model, rule (aws-key, credit-card,
custom:project-x), action as a coloured badge (BLOCKED / REDACTED / ALERT), and
a masked sample in monospace (`AKIA****************`). Filters: action, rule,
key, period. Clicking a row opens the details — the full context of the event,
without revealing the sensitive data. Empty state: "No incidents — your traffic
is clean", in a positive tone.

# Screen 4 — Policies

A per-key policy editor: detector groups (Secrets / PII / Custom rules) with
toggles and an action per group (block / redact / alert only). Custom rules: a
list of the user's own regexes and stop-words, with an add control. A preview:
"what would happen to this text" — a live check against an example.

# Screen 5 — Settings / Keys

Provider keys (OpenAI / Anthropic / Groq): masked inputs, a "configured"
indicator, Save and Clear. Mutegate keys: a list (name, masked key, created_at,
the bound policy) with Create and Delete, with confirmation. A banner when
running without a database (keys are lost on restart).

# Data to mock (the real back-end types)

StatsSummary { requests, prompt_tokens, completion_tokens, total_tokens,
  cost_usd, avg_latency_ms, error_rate }
TimeseriesBucket { ts, requests, total_tokens, cost_usd, avg_latency_ms }
ModelStat { model, provider, requests, total_tokens, cost_usd, avg_latency_ms }
LogEntry { ts, model, provider, prompt_tokens, completion_tokens, cost_usd,
  latency_ms, status_code, key_id, error }
DLPEvent { ts, key_id, model, rule, action: "blocked"|"redacted"|"alerted",
  masked_sample }
Mock it realistically: gpt-4o-mini, claude-3-5-sonnet, llama-3.3-70b; three to
five keys (ci-agent, dev-ivan, backend-prod); ten to fifteen DLP events of
different kinds.

# States and details

Loading (skeletons), empty, error; responsive from desktop to mobile;
accessibility (contrast, focus, aria); toasts on actions; human number
formatting ($0.0043, 1.2k tokens, 340 ms).

# Technical constraints

React plus TypeScript, everything in one file, inline styles or a single
`<style>`, no external libraries, fonts or images over the network (CSP-safe).
Dark and light themes through `prefers-color-scheme` plus a manual toggle.

# Response format

A short preamble first: palette, typefaces, principles. Then the working
prototype. Start with the DLP Events screen — it is the product's main
differentiator.
