# Examples

Every example needs a running gateway and your Mutegate key
(printed in the server log at startup, or set via `MUTEGATE_API_KEY`):

```bash
export OPENAI_API_KEY=sk-...          # real provider key for the gateway
go run ./cmd/mutegate                  # or: docker run (see root README)
export MUTEGATE_API_KEY=ap-...         # from the startup log
```

| File | What it shows |
|------|---------------|
| [`curl.sh`](curl.sh) | Clean / blocked / redacted requests from the shell |
| [`openai-python.py`](openai-python.py) | Official OpenAI Python SDK through the gateway (one line: `base_url`) |
| [`openai-node.mjs`](openai-node.mjs) | Official OpenAI Node SDK through the gateway |
| [`jev.sh`](jev.sh) | The Jev decision API through the gateway — allowed, redacted and blocked |
| [`seed-demo.sh`](seed-demo.sh) | Fill the incident feed with demo traffic (for screenshots/demos) |

## Pointing coding agents at the gateway

Any tool that speaks the OpenAI API works — set its base URL to the gateway:

```bash
# OpenAI-compatible tools / SDKs
export OPENAI_BASE_URL=http://localhost:8080/v1
export OPENAI_API_KEY=$MUTEGATE_API_KEY

# Claude Code and other native Anthropic clients
export ANTHROPIC_BASE_URL=http://localhost:8080
export ANTHROPIC_API_KEY=$MUTEGATE_API_KEY
```

Traffic from the agent now flows through Mutegate: secrets are blocked,
PII is redacted, and every incident lands in the DLP feed
(`/admin/dlp/events` or the web console).

## Telling agents apart

Several agents usually share one key. Send these optional headers to attribute
incidents and cost to a specific agent or run:

```
X-Mutegate-Agent:   ci-bot
X-Mutegate-Session: build-4821
```

They show up as columns and filters in the incident feed, and on the usage rows
behind the cost figures.
