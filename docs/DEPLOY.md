# Deploying Aperture on a VPS

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
