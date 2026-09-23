# DLP: detectors, policies and rolling out `block`

## What is scanned

Everything an agent sends upstream, not only the visible message: prompts,
the system prompt, text parts of multimodal messages, tool-call arguments and
tool results (where a file the agent just read ends up). The same goes for
the OpenAI Chat Completions and Responses APIs, the Anthropic Messages API and
the Jev decision API.

Detectors come in groups, and each group has one action:

| Group | Finds |
|-------|-------|
| `secrets` | AWS keys, GitHub / GitLab / Slack tokens, PEM private keys, JWTs, generic credentials |
| `pii` | Emails, payment cards, phone numbers, IBANs — plus names and addresses with the [NER stage](#names-and-addresses) |
| `custom` | Your own regexes and stop-words |

| Action | Effect |
|--------|--------|
| `block` | The request is refused with `403` (`mutegate_dlp_blocked`) and never reaches the provider |
| `redact` | The match is replaced in place — the provider receives `[REDACTED:email]` |
| `alert` | The request passes; the incident is recorded (and alerted, if configured) |
| `off` | The group is not checked |

Incidents keep a masked sample only (`AKIA****************`); raw sensitive
content is never stored.

## Policies

A policy maps groups to actions, plus custom rules and false-positive
controls. There is a default policy per organization and an optional policy
per key; changes apply immediately, without a restart.

```json
{"secrets":"block","pii":"redact","custom":"alert",
 "custom_rules":[{"name":"project-x","pattern":"project-x"}],
 "allowlist":["AKIAIOSFODNN7EXAMPLE"],
 "muted_rules":["email"],
 "scan_responses":false,
 "ner":false}
```

The console's **Policies** screen edits them with a live dry-run: paste text,
see the verdict and what the provider would receive. The same check is
`POST /admin/policies/test`.

![Policies — live dry-run](screenshots/policies.png)

## False positives

The first bad block is what makes a team switch DLP off, so there are two
escape hatches:

- `allowlist` — patterns whose matches never raise a finding (AWS's
  documented example key is the classic case);
- `muted_rules` — a detector silenced for one key, one click from the
  incident feed (`POST /admin/policies/keys/{id}/mute`).

Neither is silent: suppressed matches are still recorded as `suppressed` and
counted in `/admin/dlp/summary`.

## Before you switch to `block`

Nobody flips a DLP gateway to `block` blind. Run in `alert` for a week, read
the report, then decide. The report is the console's **Report** screen, with
Markdown and JSON export, or one call:

```bash
curl -H "Authorization: Bearer $ADMIN_API_KEY" \
  "http://localhost:8080/admin/dlp/report?period=7d"
```

It answers, per detector group, how many requests `block` would have
rejected, which rules and keys they belong to, which agent sent them, and
which policy let them through. Requests already blocked, and matches silenced
by a mute or an allowlist entry, are left out — those would not change.

## Scanning responses

By default Mutegate inspects what leaves your network. A model can also hand
a secret *back* — echoing a credential it was shown, or putting one in a tool
call the agent then runs. Set `scan_responses` on a policy (or
`DLP_SCAN_RESPONSES=true`) and the same detectors, with the same actions,
apply to the answer.

Streaming is handled: the answer is scanned through a sliding window, so a key
split across three SSE chunks is still caught. Under `redact` the text is
rewritten in flight; under `block` the stream is torn down at the first match
and the client gets an in-band error event (`mutegate_dlp_blocked`) instead
of a truncated answer; a non-streaming answer is replaced by a `403`.
Tool-call arguments are scanned as their own channel, and the Responses API's
terminal events — which repeat the full text — are rewritten too.

The cost is a fixed lag, not a slower answer: the client sees the first token
once **256 bytes** have arrived (measured: 0.88 s into a 1.46 s answer, which
still finished at 1.46 s). Nothing is buffered beyond the window. That lag is
why this is off by default.

## Names and addresses

Regexes find structured data — keys, cards, IBANs, emails. They cannot find
*Ivan Petrov* or *7 Tverskaya St*, which is the difference between a secret
scanner and DLP. That gap is closed by a local NER model running beside the
gateway, never in the cloud:

```bash
docker compose --profile ner up -d          # starts the model service
export NER_URL=http://localhost:8081
curl -X PUT http://localhost:8080/admin/policies/default \
  -H "Authorization: Bearer $ADMIN_API_KEY" -H "Content-Type: application/json" \
  -d '{"secrets":"block","pii":"redact","custom":"alert","ner":true}'
```

Findings count as PII: they follow the `pii` action, land in the feed as
`ner:person` / `ner:address` / `ner:location`, and can be muted or
allowlisted like any other rule. The gateway **refuses a `NER_URL` that is not
loopback or a private address** — shipping prompt text to a public NER API
would defeat the purpose — unless you set `NER_ALLOW_REMOTE=true`.

The model is a separate process on purpose: Mutegate stays a single static
binary with no ML runtime linked in, and `NER_URL` can point at your own
service (Presidio, GLiNER, spaCy, something internal) as long as it speaks the
contract in [`ner/README.md`](../ner/README.md). One request costs one model
call; the call is bounded by `NER_TIMEOUT_MS` (default 1000), and when the
service is unreachable the gateway keeps scanning with regexes — or refuses
the traffic, with `NER_FAIL_CLOSED=true`.

The stage is not free: a one-sentence prompt adds **26–33 ms**, a 3.5 KB
prompt **250–410 ms**, against ~2 ms for a whole request through the gateway
without it — of which the regex scan itself is ~0.25 ms on a 1.6 KB body
(`go test ./internal/inspector/ -bench ScanChatRequest`). Watch
`mutegate_ner_latency_seconds` on `/metrics`. Streaming responses are scanned
by the regex detectors only; requests and non-streaming responses get the
full stage.

## Alerts

Incidents can be sent to a webhook — plain JSON, Slack or Telegram — with a
per-key, per-rule debounce so a looping agent does not flood the channel. Each
organization sets its own in **Settings → Alerts**, which has a "send test
alert" button; `DLP_WEBHOOK_*` configures the default organization's from the
environment.
