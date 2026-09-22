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
hand. It replaces the binary by renaming over it — a running executable cannot
be written to, but it can be replaced in one step — swaps the console
directory rather than unpacking over it, reloads Caddy only when the site file
has actually changed, and then restarts the service. **If the new build does
not answer `/health` within fifteen seconds, it puts the previous binary and
console back, restarts, and fails the run.** So a bad deploy costs a minute of
errors, not an evening.

Migrations need no step of their own: the gateway brings the schema up to date
when it starts, in place, so a restart is the migration.

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
