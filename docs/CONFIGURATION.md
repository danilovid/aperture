# Configuration

Mutegate reads its configuration from environment variables. With a database,
most of what an organization changes day to day — providers, policies, limits,
alerts — lives in the console, and the environment only supplies the defaults
and the **default organization**'s starting values. See
[Providers](PROVIDERS.md) for how the two meet.

## The instance

| Variable | Meaning |
|----------|---------|
| `PORT` | Listen port (default `8080`) |
| `DATABASE_URL` | PostgreSQL. With it: accounts and organizations, persistent keys, policies, incidents and usage statistics. Without it: one in-memory gateway key, nothing survives a restart |
| `ADMIN_API_KEY` | The operator's key. Creates organizations and first invitations under `/api/instance/*`, and reaches `/admin/*` in the default organization (or the one named with `X-Mutegate-Org`). Generated and logged if unset; the admin API is never open |
| `MUTEGATE_API_KEY` | **Without a database only**: the one key agents use (generated and logged if unset). With a database, keys are created in the console or with `POST /admin/keys` |
| `MUTEGATE_ENCRYPTION_KEY` | 64 hex characters (`openssl rand -hex 32`): AES-256-GCM for provider keys and alert webhooks at rest. Gateway keys are always stored hashed |
| `REGISTRATION_OPEN` | With a database: anybody may sign up and gets an organization of their own (default `false` — invitations only) |
| `PUBLIC_URL` | The installation's public address, used to build OAuth redirect addresses |
| `OAUTH_GOOGLE_CLIENT_ID` / `OAUTH_GOOGLE_CLIENT_SECRET` | Sign-in with Google; `OAUTH_GITHUB_*` and `OAUTH_YANDEX_*` likewise. Setup in [DEPLOY.md](DEPLOY.md#signing-in-with-google-github-or-yandex) |
| `ALLOWED_ORIGINS` | CORS allowlist (default: `http://localhost:5173`, `http://localhost:4173`) |

## Providers

| Variable | Meaning |
|----------|---------|
| `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `GROQ_API_KEY` | Provider keys for the default organization. With a database they are copied into its providers on start when missing, and the console is where they live after that |
| `OPENAI_BASE_URL` | Default OpenAI address for every organization (default `https://api.openai.com`) |
| `ANTHROPIC_BASE_URL` | Default Anthropic address (default `https://api.anthropic.com`) |
| `JEV_API_KEY` / `JEV_BASE_URL` | The [Jev decision API](PROVIDERS.md#the-jev-decision-api): key from `jevai.org/agent/keys`, host (default `https://www.jevai.org`) |
| `CUSTOM_PROVIDERS` | JSON array of OpenAI-compatible upstreams — see [Providers](PROVIDERS.md#custom-openai-compatible-providers) |
| `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` | Egress proxy for every provider that has no proxy of its own |

## DLP

| Variable | Meaning |
|----------|---------|
| `DLP_ENABLED` | Outbound scanning (default `true`) |
| `DLP_SECRETS_ACTION` / `DLP_PII_ACTION` / `DLP_CUSTOM_ACTION` | `off\|alert\|redact\|block` for the default policy until one is saved (defaults `block` / `redact` / `alert`) |
| `DLP_SCAN_RESPONSES` | Also scan what the model sends back (default `false`) |
| `DLP_NER` | Turn the names-and-addresses stage on in the default policy (default `false`) |
| `NER_URL` | The local NER service, e.g. `http://localhost:8081`. Empty turns the stage off |
| `NER_TIMEOUT_MS` / `NER_MIN_SCORE` / `NER_LABELS` | Time budget per call (default `1000`), confidence floor (default `0.5`), labels to act on |
| `NER_FAIL_CLOSED` / `NER_ALLOW_REMOTE` / `NER_TOKEN` | Refuse traffic while the model is down; allow a non-local service; bearer token for it |
| `DLP_WEBHOOK_URL` / `DLP_WEBHOOK_FORMAT` / `DLP_WEBHOOK_ACTIONS` / `DLP_WEBHOOK_CHAT_ID` | Alerts for the default organization: `json`/`slack`/`telegram`, which actions alert (default `blocked`). Other organizations set theirs in Settings → Alerts |

What each of these does is in [DLP](DLP.md).

## Limits

| Variable | Meaning |
|----------|---------|
| `LIMIT_BUDGET_DAILY_USD` | Default daily spend ceiling per key (empty = none) |
| `LIMIT_REQUESTS_PER_MINUTE` | Default request rate ceiling per key (empty = none) |

## Names from before the rename

The project used to be called Aperture. `APERTURE_API_KEY` and
`APERTURE_ENCRYPTION_KEY` are still read when the new names are unset, with a
warning in the log; `X-Aperture-*` request headers are accepted alongside
`X-Mutegate-*`.
