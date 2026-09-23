# Deploying Aperture on a VPS

Two ways in. **Docker Compose** is the quick start below — one command, good
for trying it out. **The pipeline** is what the project's own installation
uses: a merge to `main` puts the new build on the server by itself. That is
described at the end, under [Continuous deployment](#continuous-deployment).

## Quick start

On the server:

```bash
# 1. Clone the repository
git clone https://github.com/danilovid/aperture.git
cd aperture

# 2. Start it
docker compose -f docker-compose.prod.yml up -d --build

# 3. Open it in a browser
# http://YOUR_IP/
```

## What you get

| Service  | Port | Description                          |
|----------|------|--------------------------------------|
| Web UI   | 80   | The console                          |
| Aperture | 8080 | The API (also reachable under `/api`) |

Everything under `/api/*` is proxied to Aperture.

## Configuration

### Your own domain

1. Point the domain at the server's IP in DNS.
2. Add HTTPS (Caddy, or nginx plus certbot).
3. Serving the API from another domain needs CORS — Aperture already has it.

### Your own API URL

The web build takes the API URL as a build argument:

```bash
docker compose -f docker-compose.prod.yml build \
  --build-arg VITE_APERTURE_URL=https://api.example.com web
```

### Aperture environment variables

Add them in `docker-compose.prod.yml`, for example:

```yaml
aperture:
  environment:
    PORT: 8080
    OPENAI_BASE_URL: https://api.openai.com  # optional
```

The OpenAI key is set from the admin panel in the UI.

## Logs

```bash
docker compose -f docker-compose.prod.yml logs -f
```

## Stopping

```bash
docker compose -f docker-compose.prod.yml down
```


---

## Continuous deployment

A merge to `main` deploys. The `deploy` job in `.github/workflows/ci.yml` runs
after both halves of CI have passed, and only for pushes to `main`, so nothing
reaches the server that has not been built, vetted and tested first — against
a real PostgreSQL, since that is where the isolation rules live.

What it does, in order:

1. builds the gateway for `linux/amd64`, stamping `main-<sha>` as the version,
   so `aperture starting` in the journal says which commit is running;
2. builds the console with the installation's own origin;
3. copies both, plus `deploy/aperture.caddy` and `deploy/install.sh`, into the
   deploy account's `~/incoming`;
4. runs `install.sh` on the server;
5. asks `https://<domain>/health` until it answers, and fails the run if it
   never does.

`deploy/install.sh` is the part that touches the machine, and it lives in the
repository rather than the workflow so it can be read, reviewed and run by
hand. It stages everything beside the live files first, then swaps things in
one at a time — routes, binary, console — and restarts. **If anything fails
after the first swap, a trap on exit puts back every swapped thing**: a Caddy
config that does not load, a binary that does not answer `/health` within
fifteen seconds, anything. So a bad deploy costs a minute, not an evening.

A few details that are there on purpose:

- **The routes go first.** New routes in front of the old binary are harmless;
  a new binary behind old routes has endpoints nobody can reach.
- **Caddy is validated by reloading it**, through systemd. The main Caddyfile
  on this machine reads its domains from the Caddy unit's environment, so
  `caddy validate` from a shell expands them to nothing and fails on a config
  that is perfectly fine. The reload runs in the right environment, and it is
  transactional: a config Caddy cannot load leaves the running one in place.
- **The binary is renamed over, not written to.** A running executable cannot
  be written, but it can be replaced in one step.
- **The console is swapped as a directory**, so there is never a half-extracted
  site and no file from an older build lingers.
- **The previous build stays** as `aperture.prev` and `aperture-console.prev`
  until the next deploy, as a way back by hand.

`deploy/install_test.sh` runs every one of those endings — upgrade, unchanged
routes, a Caddy config that does not load, a build that does not come up, a
first install, a first install that does not come up — in a throwaway
container, and CI runs it on every change. The rollback paths are the ones
nobody exercises by hand, so they are exercised there.

### The database is the one thing a rollback does not undo

The gateway brings its schema up to date when it starts, in place and forward
only, so a restart is the migration — and a rollback puts the previous binary
back onto the new schema. Mostly that is fine: the migrations are written so
that an older binary keeps working between steps. Not always: after
multi-tenancy, for instance, `dlp_policies` is keyed by `(org_id, name)`, and a
build from before it would fail on its `ON CONFLICT (name)`.

So before merging a release that changes the schema, take a backup on the
server:

```bash
set -a; . /etc/aperture/db.env; set +a
pg_dump "$DATABASE_URL" | gzip > /var/backups/aperture/pre-$(date -u +%Y%m%dT%H%M%SZ).sql.gz
```

### The first sign-in

With a database, the console is signed into with an account, not the admin
key, and accounts arrive only by invitation. The operator — whoever holds
`ADMIN_API_KEY` — sends the first one.

An installation that ran before multi-tenancy keeps all its keys and incidents
in the **default** organization, which a migration created and nobody was ever
invited to. Invite its first owner:

```bash
curl -X POST https://<domain>/api/instance/organizations/00000000-0000-0000-0000-000000000001/invitations \
  -H "Authorization: Bearer $ADMIN_API_KEY" -H "Content-Type: application/json" \
  -d '{"email":"you@company.com"}'
```

A new installation creates its first organization and owner in one step:

```bash
curl -X POST https://<domain>/api/instance/organizations \
  -H "Authorization: Bearer $ADMIN_API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"Acme","owner_email":"you@company.com"}'
```

Either answers with a `link`. Open it, choose a password, and you are in; from
there, everybody else is invited from the console's Members screen. The link
works once, for that address, for seven days.

### Signing in with Google, GitHub or Yandex

Optional, and off until configured. Each provider needs an application
registered with it, and the gateway needs to know its own public address,
because a provider only sends people back to the exact redirect address
registered with it:

```bash
PUBLIC_URL=https://aperture.example.com
OAUTH_GOOGLE_CLIENT_ID=…      OAUTH_GOOGLE_CLIENT_SECRET=…
OAUTH_GITHUB_CLIENT_ID=…      OAUTH_GITHUB_CLIENT_SECRET=…
OAUTH_YANDEX_CLIENT_ID=…      OAUTH_YANDEX_CLIENT_SECRET=…
```

Configure any subset; the sign-in page shows a button for each one set.

| Provider | Where to register | Redirect address to enter |
|----------|-------------------|---------------------------|
| Google | Google Cloud Console → APIs & Services → Credentials → OAuth client ID, type *Web application* | `https://<domain>/api/auth/oauth/google/callback` |
| GitHub | Settings → Developer settings → OAuth Apps → New OAuth App | `https://<domain>/api/auth/oauth/github/callback` |
| Yandex | oauth.yandex.ru → Create app → *Web services*; grant access to the email address and to the name | `https://<domain>/api/auth/oauth/yandex/callback` |

Accounts stay by invitation. A provider signs somebody in only when it can be
tied to a person this installation already knows: an identity connected
before; an invitation whose address the provider verifies; or an existing
account with the same address, *if the provider says the address is
verified*. A stranger with a Google account is turned away, and so is an
address the provider has not verified — otherwise anybody could register
somebody else's address at a provider and walk into their account.

Without `PUBLIC_URL` the gateway builds the redirect address from the host
each request arrives on, and logs a warning at startup. That works only when
the host is exactly the one registered with the provider.

### Providers, proxies and what the environment still decides

With a database, each organization sets up its own providers in the console
(Settings & Keys → Providers): address, key, an optional proxy per provider,
a timeout, and a connectivity check before saving. The environment keeps two
roles:

- `OPENAI_BASE_URL`, `ANTHROPIC_BASE_URL`, `JEV_BASE_URL` are the default
  addresses for every organization that has not set its own;
- `OPENAI_API_KEY` and the other provider keys, `CUSTOM_PROVIDERS`, and the
  alert webhook `DLP_WEBHOOK_URL` belong to the **default organization only**.
  Keys and custom providers are copied into its providers on start when
  missing; after that the console is where they live.

`HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` still apply to every provider without a
proxy of its own. A provider's own proxy is used for all of its traffic,
`NO_PROXY` notwithstanding. If the proxy intercepts TLS, its CA has to be in
the system trust store; the connectivity check says so when it is not.

### The routes are versioned with the code

`deploy/aperture.caddy` is the site file Caddy serves this installation with.
It is in the repository because the routes are part of the code: an endpoint
added in Go and not added there falls through to the console and answers a
React page to an API call. `TestEveryRouteIsProxiedInProduction` compares the
two and fails the build rather than letting that ship.

### One-time setup

On the server, as root, with the public half of a key generated for this:

```bash
./deploy/setup-server.sh "$(cat id_deploy.pub)"
```

It creates the `aperture-deploy` account, its `authorized_keys`, and a
`/etc/sudoers.d/aperture-deploy` listing what the deploy may run. It prints
the host key to pin. To undo all of it:

```bash
rm /etc/sudoers.d/aperture-deploy && userdel -r aperture-deploy
```

Then in the repository (Settings → Secrets and variables → Actions):

| Name | Kind | What |
|------|------|------|
| `DEPLOY_SSH_KEY` | secret | the private half of that key |
| `DEPLOY_HOST` | variable | the server's address |
| `DEPLOY_USER` | variable | `aperture-deploy` |
| `DEPLOY_DOMAIN` | variable | the domain the installation answers on |
| `DEPLOY_KNOWN_HOSTS` | variable | the host key line, so the pipeline pins it |

`DEPLOY_KNOWN_HOSTS` is not decoration. Without a pinned host key the first
connection trusts whatever answers on that address, which is the attack the
pinning exists to stop.

### What that account can do

Replace the gateway binary, replace the console, rewrite Aperture's Caddy site
file, restart those two services. The sudoers file lists the commands one by
one, and that list is worth having — it says what a deploy is supposed to
touch, and anything else needs somebody to go and add it deliberately. It is
not, however, a security boundary: an account that can replace a binary root
runs can run anything root can. **Treat the SSH key as a production
credential.** If the repository is ever compromised, rotate it the way you
would rotate a database password: generate a new pair, re-run
`setup-server.sh`, replace `DEPLOY_SSH_KEY`.

The deploy job is bound to a `production` GitHub environment, so a required
reviewer can be added there if a merge should not be enough on its own.
