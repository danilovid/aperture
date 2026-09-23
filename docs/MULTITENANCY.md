# Aperture — accounts, organizations and data isolation

The project is moving from "one installation, one admin key" to a product with
people, organizations and registration from outside. This document fixes the
decisions before the code: in a multi-tenant system an isolation mistake costs
more than any feature.

Status: accepted 2026-09-22. The earlier [`AUTH_AND_ACCESS.md`](AUTH_AND_ACCESS.md)
describes authenticating **machines** (the keys agents use) and still stands —
this one is about **people**.

---

## 1. What actually changes

Today nothing in the system knows whose data it is. Six tables — `api_keys`,
`dlp_events`, `dlp_policies`, `key_limits`, `provider_keys`, `request_logs` —
are global to the installation, and the whole admin API is guarded by a single
static `ADMIN_API_KEY` that sits in the user's localStorage.

The login form is the easy half. The hard half is that **every table and every
admin request has to become scoped to an organization**. The incident feed
holds masked samples of somebody's data, agent names, key identifiers and
spend: one unscoped `SELECT` is a leak between customers, not a cosmetic bug.

Hence the principle this document is built on: **isolation is enforced by types
and tests, not by discipline**. A query without an `org_id` should be hard to
write and impossible to miss.

---

## 2. Data model

### 2.1 New tables

```sql
organizations   (id, name, slug UNIQUE, created_at, deleted_at)
users           (id, email UNIQUE, password_hash NULL, name,
                 email_verified_at, created_at, last_login_at, disabled_at)
user_identities (id, user_id, provider, provider_user_id, email, created_at,
                 UNIQUE(provider, provider_user_id))
memberships     (org_id, user_id, role, created_at, PRIMARY KEY(org_id, user_id))
sessions        (id, user_id, current_org_id, token_hash UNIQUE, created_at,
                 expires_at, last_seen_at, ip, user_agent, revoked_at)
invitations     (id, org_id, email, role, token_hash UNIQUE, invited_by,
                 created_at, expires_at, accepted_at)
audit_log       (id, org_id, actor_user_id, actor_kind, actor_id, actor_label,
                 action, target, meta JSONB, ip, created_at)
```

`password_hash` is nullable: somebody who arrived through an identity provider
may have no password at all. `user_identities` lives beside the user rather
than inside it, because one person attaches Google today and GitHub tomorrow.

### 2.2 Existing tables

Each gains `org_id UUID NOT NULL REFERENCES organizations(id)` and an index on
it — in `dlp_events` and `request_logs` a composite one with `ts DESC`, since
the feed and the statistics are always read as "the last N in this
organization".

Existing installations migrate in place, like everything else in this project
(`ALTER TABLE ... ADD COLUMN IF NOT EXISTS`):

1. create a `default` organization and an owner from the `ADMIN_API_KEY` install;
2. set that `org_id` on every existing row;
3. only then `SET NOT NULL`.

The order matters: between steps 1 and 3 an older binary keeps working.

### 2.3 Where the organization comes from

| Who is calling | Source of the organization |
|----------------|----------------------------|
| A person in the console | the session (`sessions.current_org_id`) |
| An agent on `/v1/*`, `/api/v1/decisions*` | the aperture key → `api_keys.org_id` |
| CI or a script | a service token → its `org_id` |
| The operator of the installation | instance admin, see §4.3 |

Agents do not log in and do not choose an organization: it follows from the key
they authenticated with. That is also what makes "one organization's key wrote
an event into another's data" impossible.

---

## 3. How isolation is enforced

Three layers, each catching what the previous one missed.

**Layer 1. Method signatures.** Every store method takes the organization as
its first argument: `List(ctx, orgID, filter)`. An unscoped call does not
compile.

**Layer 2. A guard test.** `TestEveryQueryIsScopedByOrganization` walks the
sources of `internal/storage/postgres` and requires every `SELECT`/`UPDATE`/
`DELETE` against a table with an `org_id` to carry a condition on it — a
condition, not a mention, so `org_id::text` in a select list does not count.
Inserts must name `org_id` in their column list, or the row would be filed
under the column default. Two statements are exempt, each listed in the test
with its reason: the lookup that authenticates a token (which is what decides
the organization) and the migration that retires the shared `dev` key.
`TestEveryScopedTableIsMigrated` adds the companion check that each scoped
table actually gets its `org_id` column, in the same file that declares it —
it caught `key_limits` being declared and never migrated.

**Layer 3. Row-level security in PostgreSQL** (after the first release).
`ALTER TABLE ... ENABLE ROW LEVEL SECURITY` plus `SET LOCAL app.org_id` inside
the transaction. With a pgx pool this needs care — the value must be set on the
same connection as the query — so it lands as its own step and only as defence
in depth, never instead of layers 1 and 2.

**Layer 0, under all of them: the stores themselves.**
`internal/storage/storagetest` holds one tenancy contract run against both
implementations, because a rule PostgreSQL enforces and memory does not is a
rule most of the suite never sees. It covers the feed, filters that must not
reach across organizations, counts, the report, and two organizations using
the same key name. Against PostgreSQL it also runs the upgrade from the
pre-tenancy schema: old rows land in the default organization rather than being
lost.

Separately: **reports and exports**. `/admin/dlp/report`, `/admin/stats/*` and
any export compute aggregates, which is exactly where a lost filter hides best.
`internal/server/tenancy_test.go` signs two owners in and asks each endpoint as
both, including the write path — a request carries no organization of its own,
the key does, so the rows it writes must land in that key's organization.
`X-Aperture-Org` is honoured only for the operator's key, never for a session.

---

## 4. Authenticating people

### 4.1 Email and password

- Hashing is **argon2id** (`golang.org/x/crypto/argon2`): 64 MB, 3 passes, up
  to 4 threads, a 16-byte salt. `golang.org/x/crypto` was already in the tree
  as an indirect dependency of pgx (SCRAM), but argon2 also pulls in
  `golang.org/x/sys`: blake2b reaches for `x/sys/cpu` on amd64 to pick its
  AVX2 path. So the cost is one added module, not none. The stdlib alternative is
  `crypto/pbkdf2` (Go 1.24+) at 600k iterations: weaker against GPUs, and a
  local change in one file if we ever want to shrink the dependency list.
- The parameters are stored inside the hash, so raising them does not
  invalidate existing passwords: the hash is recomputed on the next successful
  login (`NeedsRehash`).
- "Wrong password" and "no such user" answer with the same text **and the same
  timing**. Otherwise the login form is an email enumerator.
- Attempt limiting is per (IP, email) with exponential backoff and a lockout
  after a run of failures — the same approach as `internal/limits`.

### 4.2 OAuth: Google, GitHub, Yandex

Authorization code flow with PKCE, written against the standard library in
`internal/oauth`: no library is added. The flow:

1. The console `POST`s `/api/auth/oauth/{provider}/start` and gets back the
   provider's address. A `POST` rather than a redirecting `GET`, so a link on
   another site cannot start a flow in somebody's browser.
2. The state it has to remember — nonce, PKCE verifier, where to go next, an
   invitation being redeemed, the person connecting an account — travels in an
   HttpOnly cookie, HMAC-signed with a key derived from an installation
   secret, valid for ten minutes, single-use.
3. `GET /api/auth/oauth/{provider}/callback` checks the cookie against the
   returned `state`, exchanges the code with the verifier, asks for the
   profile, decides whose account it is, and redirects. Failures come back to
   the page that started the flow as a code (`?oauth_error=no_account`), never
   as provider text.

Whose account a provider sign-in is, in order:

| Situation | Outcome |
|-----------|---------|
| The identity was connected before | signs its person in |
| An invitation is being redeemed | the provider's **verified** address must be the invited one; the account is created without a password, or found, and joins |
| No invitation, a **verified** address matching an existing account | connects to it and signs in — the only automatic linking |
| Signed in, connecting from the account page | links to that person whatever the address: they just proved they hold both |
| Anything else | refused: accounts are by invitation |

Linking to an existing account happens **only on a verified email** from the
provider. Otherwise an account can be taken over by registering somebody else's
unverified address with a provider. GitHub's verification comes from its
emails list, never from the public `/user` address. Yandex has no flag; it
only lists addresses a person has confirmed, so a present `default_email` is
treated as verified — written down in `yandexProfile`, not implied.

The last way to sign in cannot be disconnected: a person with no password and
one identity keeps it.

### 4.3 Sessions and CSRF

- The session token is 32 random bytes; the database stores only its `sha256`.
- Cookie: `HttpOnly`, `Secure`, `SameSite=Lax`, 30 days, extended on activity.
- CSRF: `SameSite=Lax` plus a mandatory `X-Aperture-CSRF` header on every
  mutating request. For a same-origin SPA that is enough — a header is exactly
  what a cross-site form post cannot set.
- "Sign out everywhere" revokes every session of the user.

### 4.4 What happens to ADMIN_API_KEY

It stops being the key to all data and becomes the **instance admin** — the
operator's credential: create an organization, invite the first owner into one
that already exists (above all the default organization an upgrade migrates
everything into, which nobody was ever invited to), restore one that was
closed, check the health of the installation. It has no organization
of its own, so to read one it must name it with `X-Aperture-Org`, and that is
logged.

### 4.5 Service tokens

For CI and scripts, an organization issues **service tokens**: `apt_…`, stored
hashed, carrying scopes rather than a person. Two gates apply to every request
they make, and they are different questions:

- **the scope the endpoint needs**, from one table in `service_tokens.go`
  keyed by the mux's own route patterns. An endpoint that is not in the table
  cannot be reached by a token at all — members, invitations, organization
  settings and the tokens themselves are a person's business. A test checks
  the table against the routes, because a renamed route would otherwise
  silently close a door.
- **the role the endpoint asks for**, which a token satisfies through the
  strongest role its scopes imply (`events:read` → viewer, `keys:write` →
  admin). That means every existing check — `requireRole`, `adminOrg` —
  applies to a token unchanged, and a token is never stronger than its scopes.

Scopes: `events:read` (feed, reports, statistics, policy testing),
`keys:read`, `keys:write` (keys and provider credentials), `policies:write`
(policies, limits, muting). A token with no scopes is refused at creation
rather than quietly meaning "all" or "none". Tokens expire — ninety days
unless the caller says otherwise — and their last use is recorded, so an
unused one can be found and removed.

---

## 5. Roles

| Role | May |
|------|-----|
| **owner** | everything, plus billing, deleting the organization, handing it over |
| **admin** | keys, policies, limits, providers, invitations, service tokens |
| **member** | read the feed and reports, use the playground, own keys |
| **viewer** | read only: dashboards, feed, reports |

Permission checks live in one place (`requireRole`), not scattered across
handlers. A role never grants access to another organization: membership is
checked first, the role second.

---

## 6. Providers and the per-provider proxy

Each organization owns its upstreams, in `providers (org_id, name, kind,
base_url, api_key, proxy_url, prefixes, timeout_ms, enabled, …)`:

- **Built-ins** (`openai`, `anthropic`, `groq`, `jev`) exist at most once per
  organization and are named after their kind — the same name an aperture
  key's own provider keys are filed under.
- **OpenAI-compatible** providers have free names and claim models by prefix;
  the longest prefix wins, and two providers cannot claim the same one.
- `api_key` and `proxy_url` are sealed with `APERTURE_ENCRYPTION_KEY`, the
  same AES-256-GCM as the provider keys: a proxy address carries a password as
  often as not. The console is only ever told whether they are set.

**Every path that reaches an upstream** — chat completions, the native
Anthropic Messages and OpenAI Responses APIs, Jev — resolves through one
function (`upstreamFor`), so they cannot disagree. For a request from an
aperture key in organization O:

1. the provider is O's row for the model (by prefix, else by kind); a disabled
   one refuses, even for a key carrying its own credential;
2. the key is the aperture key's own for that provider, else **the
   organization's** — which is what finally makes Settings' provider keys
   reach traffic;
3. the address is the provider's, else the installation's default
   (`OPENAI_BASE_URL` …), else the documented one;
4. the transport comes from a cache keyed by proxy and timeout. Before this
   every request built a new `http.Client`, throwing its connection pool away;
   now a provider's connections are reused, and providers sharing a proxy
   share a pool.

Without a database none of this exists: the environment decides for everybody,
exactly as before.

**Saving offers a connectivity check** (`POST /admin/providers/test`): one
read-only call — the model list where there is one — through the configured
proxy, with the step that failed named: the proxy, the connection, TLS, the
key, or an unexpected answer. It tests what is on screen, saved or not, and
saves nothing. Jev has nothing to read without spending a decision, so for Jev
it proves only that the address answers.

**The environment seeds the default organization only.** `OPENAI_API_KEY` and
friends and `CUSTOM_PROVIDERS` become the default organization's providers
when those rows are missing, and never overwrite what was set in the console.
Other organizations get none of it: an operator's OpenAI key being spent by
every tenant is a billing decision, not a default. The keys saved on the old
Settings row are adopted onto providers once, at the first start.

---

## 7. Gateway settings in the interface

Moving from environment variables into the database (per organization, with the
environment as the default): provider base URLs, custom OpenAI-compatible
endpoints, timeouts, default DLP actions, the alert webhook, limits. Done:
providers (§6), the default policy and limits (already per organization since
slice 3), and alerts.

**Alerts are per organization.** Before this, one webhook received every
organization's incidents — another tenant's rule names, key ids and agents in
the operator's channel. Now each organization sets its own, stored encrypted
(a Slack or Telegram address is the credential to post to it), and
`DLP_WEBHOOK_URL` belongs to the default organization only. An organization
with nothing of its own sends nowhere.

Staying **instance-level** (environment, operator's business): `DATABASE_URL`,
`APERTURE_ENCRYPTION_KEY`, `NER_URL` (a separate service the operator runs),
`PORT`, `ALLOWED_ORIGINS`, SMTP.

---

## 8. Landing page and routing

One SPA, three kinds of visitor. On load it asks one question — `GET
/api/auth/me` — and the answer decides what it shows:

| Answer | Who | What they see |
|--------|-----|---------------|
| `200` | signed in | the console, in the organization on their session |
| `401` | a visitor | the landing page, sign-in, invitations |
| `503` | an installation without a database, so without accounts | the console as it always was, with the admin key typed into Settings |

The third keeps `docker run` working exactly as before: accounts only exist
where there is somewhere to keep them.

- The router is hand-written on the History API (`web/src/router.ts`, a
  `useSyncExternalStore` over `popstate`). `react-router` is not added: the
  project's dependencies are `react` and `react-dom`, and there are a dozen
  routes and no nested layouts.
- Routes: `/` (landing), `/login`, `/invite/{token}`, `/app/{screen}`.
  There is no `/register`: registration is by invitation, so the invitation
  page *is* the registration page, and it knows who it is for. OAuth adds
  `/oauth/{provider}/callback` with slice 6.
- `/invite/{token}` first asks `POST /api/invitations/lookup` what the
  invitation is for, without using it up, so the page says "join Acme as a
  viewer" before asking for a password. The token travels in the body, since
  paths end up in access logs. Then it shows one of four things: a
  registration form for a new address, sign-in-and-join for an existing
  account, one-click join for the right person already signed in, and "this
  is for someone else" for the wrong one.
- The server serves no data without a session — the landing page is not what
  stands between a stranger and the data. `/app/*` without a session gets the
  same SPA, the API answers `401`, and the SPA sends them to
  `/login?next=…` and back afterwards.
- A `401` in the middle of a session is only a hint: some endpoints answer
  `401` to a session simply because they are not for sessions (the operator's
  alert settings). So the console asks `/api/auth/me` before deciding the
  session is gone.
- The console is keyed by organization: switching remounts it, so nothing
  fetched for one organization is ever drawn under another's name.
- The console talks to its own origin, in production (Caddy) and in
  development (Vite proxies the same paths), so the session cookie never
  crosses origins. CORS still allows credentials for the allowlisted origins,
  for a split deployment.

---

## 9. Auditing what people do

Agent traffic is already recorded. Corporate accounts need a second journal —
**which person did what**: created or revoked a key, weakened a policy, unmuted
a rule, invited a member, changed a role, edited a provider. It is the first
thing asked about in a review, and the thing that saves the investigation when
something goes wrong.

Written to `audit_log`, shown on its own tab, available to owner and admin.

**Who.** Three kinds of actor change an organization, and the entry says which:
a person (`user`, named by email), a service token (`token`, named by the
name it was given) and the installation's operator (`operator`, with
`ADMIN_API_KEY`). The name is stored on the entry, not looked up: a journal
that forgets who did something once they have left is useless for exactly the
question it is kept to answer. `actor_user_id` still links to the person
while they exist and goes `NULL` when they are deleted.

**What.** An action is a dotted code whose first part is its group, and the
tab filters by group:

| Group | Actions |
|-------|---------|
| `key` | `create`, `delete` |
| `policy` | `update` (default or one key's), `reset` (a key back on the default), `mute`, `unmute` |
| `limits` | `update`, `reset` |
| `provider` | `create`, `update`, `delete` — including keys saved through `/admin/config` |
| `alerts` | `update` |
| `member` | `invite`, `uninvite`, `join`, `role`, `remove`, `leave` |
| `token` | `create`, `revoke` |
| `organization` | `create`, `rename`, `delete`, `restore` |

A save records what moved — `secrets: block → alert`, `muted: email`,
`daily budget: $25 → $100`, `proxy replaced` — rather than two JSON blobs to
diff by eye. A change that lets more through is marked `weakened`: an action
made less strict, a custom rule removed, an allowlist entry or a muted rule
added, response scanning or name detection turned off, a budget or rate limit
raised or lifted. For a key's own policy the comparison is against what the
key was actually held to, so giving one key a laxer policy of its own reads
as the weakening it is.

**Never a secret.** Provider keys, proxy addresses (they carry passwords),
webhook paths (a Slack hook or a Telegram bot token is the path) and tokens
are recorded as *set*, *replaced* or *removed*; a webhook as its scheme and
host. A test reads the whole journal back after saving each kind of secret
and looks for them.

**When.** After the change has landed, and only then: a refused or failed
request changed nothing, and a save that changed nothing is not recorded
either. Writing the entry does not fail the request — the change is already
made, and answering "error" about it would send somebody to make it twice —
but a failed write is logged as an error.

`GET /admin/audit?group=&before=&limit=` pages newest first. Owners and admins
read it, and the operator naming an organization with `X-Aperture-Org`.
Service tokens do not, whatever their scopes: a journal of who did what is
what a leaked token should not be able to read to learn whom to impersonate.
The store is append-only; there is no API to change or remove an entry.

---

## 10. Order of work

| # | Slice | Contents |
|---|-------|----------|
| 1 | Foundation ✅ | schema, migration, `internal/auth` (argon2id, sessions), account and organization stores |
| 2 | Sign-in ✅ | `/api/auth/*`: registration, login, logout, `me`; session middleware and CSRF; attempt limiting |
| 3 | Isolation ✅ | `org_id` through every existing table and method; the guard test; two-organization tests on reports and statistics |
| 4 | Organizations ✅ | invitations, roles, `requireRole`, switching organization, service tokens |
| 5 | Interface ✅ | router, landing page, login and registration forms, members screen |
| 6 | OAuth ✅ | Google, GitHub, Yandex |
| 7 | Providers ✅ | the providers table, per-provider proxy, connectivity check, gateway settings in the UI |
| 8 | Audit ✅ | the journal of human actions and its tab |

Slices 1–4 change both the API contract and the schema, so they run back to
back without a pause: a half-multi-tenant system is worse than either extreme.

---

## 11. Decisions taken

- **Registration is by invitation.** It is closed to the outside; an admin
  creates an invitation and passes the link along.
- **Email and password only, with no verification mail** for now, so nothing
  depends on SMTP. Password reset and invitation email wait for a mail path.
- **No billing** in the first version; organizations are unlimited.
- **Deleting an organization is soft**, with `deleted_at` and a recovery
  window. (Worth revisiting: incidents hold samples of somebody else's data,
  and those are better deleted for real.) Soft does not mean half-closed:
  a deleted organization's console, agent keys and service tokens all stop
  working, because a deletion that left traffic flowing would not be one.
  Restoring is the operator's job — nobody inside a closed organization can
  sign in to undo it.
- **An organization is renamed, never re-slugged.** The slug is what other
  things point at.
- **Machines carry scopes, people carry roles**, and the two meet in one
  place. "CI may rotate keys" is a narrower thing to write down than "CI is an
  admin", and the narrow one is what you want on record when a token leaks.

- **The login identifier is the email address.** No separate username, so
  there is one thing to type and one thing to prove.
- **Budgets are counted per (organization, key)**, not per key. Key ids are
  unique on their own today; composing the counter key means a future id
  scheme that only promises uniqueness within an organization cannot quietly
  make two tenants share a budget.

## 12. Still open

- [ ] SMTP: whose (Resend, Postmark, an own relay), and what happens in a
      closed network with no mail at all — invitations as links copied by hand?
- [x] ~~**What Settings → provider keys is for, now that the `dev` key is gone.**~~
      Resolved in slice 7: they are the organization's providers' keys, the
      default every aperture key inherits.
      Those keys live on a per-organization row that used to be reachable as
      the bearer token `dev` — which is exactly why it was retired. On a
      PostgreSQL installation nothing reads them any more: every aperture key
      carries its own provider credential. Either they become the default a
      key inherits when it has none, or the screen goes.
- [x] ~~**Alerts are still instance-wide.**~~ Resolved in slice 7, and it
      was worse than stated: the one webhook received every organization's
      incidents. `/admin/alerts` runs on the
      operator's key and one webhook serves the whole installation, so a
      signed-in owner cannot see or set their own. It belongs with the
      providers slice, where per-organization settings get a home.
