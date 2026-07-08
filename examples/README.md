# Examples

Every example needs a running gateway and your aperture key
(printed in the server log at startup, or set via `APERTURE_API_KEY`):

```bash
export OPENAI_API_KEY=sk-...          # real provider key for the gateway
go run ./cmd/aperture                  # or: docker run (see root README)
export APERTURE_API_KEY=ap-...         # from the startup log
```

| File | What it shows |
|------|---------------|
| [`curl.sh`](curl.sh) | Clean / blocked / redacted requests from the shell |
| [`openai-python.py`](openai-python.py) | Official OpenAI Python SDK through the gateway (one line: `base_url`) |
| [`openai-node.mjs`](openai-node.mjs) | Official OpenAI Node SDK through the gateway |
| [`seed-demo.sh`](seed-demo.sh) | Fill the incident feed with demo traffic (for screenshots/demos) |

## Pointing coding agents at the gateway

Any tool that speaks the OpenAI API works — set its base URL to the gateway:

```bash
# OpenAI-compatible tools / SDKs
export OPENAI_BASE_URL=http://localhost:8080/v1
export OPENAI_API_KEY=$APERTURE_API_KEY
```

Traffic from the agent now flows through Aperture: secrets are blocked,
PII is redacted, and every incident lands in the DLP feed
(`/admin/dlp/events` or the web console).
