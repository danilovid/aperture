# Connecting your tools

<!-- Generated from web/src/connect/guides.ts by `npm run docs:connect`. Edit that file, not this one. -->

Point the tool at the gateway instead of the provider, with a Mutegate key instead of the
provider's; the provider's own key stays on the gateway. The snippets below use
`http://localhost:8080` for the gateway and `ap-your-key` for the key. Every installation serves
the same guides at `/connect`, and the console shows them under Settings → API keys with
its own address and your key filled in.

## What happens to the traffic

**Every request is read on the way out.** Prompts, the system prompt, tool-call arguments and tool results — where a file the agent just read ends up — are scanned before anything reaches the provider. Clean traffic passes through untouched, streaming included.

**Personal data is redacted in place.** The provider receives [REDACTED:email] instead of the address. The tool notices nothing: the request goes through, and the answer comes back as usual.

**Secrets are blocked.** The request never reaches the provider, and the tool gets an HTTP 403 saying which rule stopped it — on the Anthropic API a permission_error, on the OpenAI APIs an error of type mutegate_dlp_blocked. Most tools show it as an API error.

**Tell agents apart.** Several agents usually share a key. Where a tool can send headers, X-Mutegate-Agent names the agent and X-Mutegate-Session the run, and both show up on incidents and on the cost figures.

## Coding agents

[Claude Code](#claude-code) · [Codex CLI](#codex-cli) · [Cursor](#cursor) · [Cline](#cline) · [Continue](#continue) · [Aider](#aider) · [OpenCode](#opencode)

### Claude Code

*Anthropic Messages API · /v1/messages*

Claude Code speaks the Anthropic Messages API, which the gateway serves natively. Two environment variables, no code change.

In the shell:

```bash
export ANTHROPIC_BASE_URL=http://localhost:8080
export ANTHROPIC_AUTH_TOKEN=ap-your-key
claude
```

Or for good — `~/.claude/settings.json`:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://localhost:8080",
    "ANTHROPIC_AUTH_TOKEN": "ap-your-key"
  }
}
```

- The gateway recognizes Claude Code: incidents and costs land as agent claude-code, one session per Claude Code session, with nothing to set. To name it otherwise, add ANTHROPIC_CUSTOM_HEADERS='X-Mutegate-Agent: name'.
- ANTHROPIC_AUTH_TOKEN is sent as a Bearer token and needs no approval; ANTHROPIC_API_KEY works too, as x-api-key, and asks once.
- Keep the key in ~/.claude/settings.json or .claude/settings.local.json, never in the .claude/settings.json a repository commits. In the VS Code extension the same variables go in claudeCode.environmentVariables.
- A blocked request shows as "API Error: 403 request blocked by DLP policy: sensitive data detected (…)". Claude Code puts "Failed to authenticate" in front of it; the rule named at the end is the real reason.
- Token counting (/v1/messages/count_tokens) is not served. Claude Code falls back to an estimate, so /context shows approximate numbers; nothing else changes.
- With the Mutegate CLI (github.com/Mutegate/cli): mutegate login, then mutegate run -- claude. Nothing is written anywhere, and every run is its own session on incidents and costs.

[Claude Code documentation](https://code.claude.com/docs/en/llm-gateway-connect)

### Codex CLI

*OpenAI Responses API · /v1/responses*

Codex speaks the OpenAI Responses API, which the gateway serves at /v1/responses. Add the gateway as a model provider and make it the default.

`~/.codex/config.toml`:

```toml
model = "gpt-5"
model_provider = "mutegate"

[model_providers.mutegate]
name = "Mutegate"
base_url = "http://localhost:8080/v1"
env_key = "MUTEGATE_API_KEY"
wire_api = "responses"
http_headers = { "X-Mutegate-Agent" = "codex" }
```

Then:

```bash
export MUTEGATE_API_KEY=ap-your-key
codex
```

- The provider has to be defined in ~/.codex/config.toml: a project's .codex/config.toml cannot change model_provider.
- env_key names the variable Codex reads the key from, and sends as a Bearer token.
- With the Mutegate CLI (github.com/Mutegate/cli), mutegate connect codex writes this file for you, and mutegate run -- codex starts it with the key and a session of its own.

[Codex CLI documentation](https://developers.openai.com/codex/config-advanced)

### Cursor

*OpenAI Chat Completions · /v1/chat/completions*

Cursor takes an OpenAI key and a base URL override in its settings. Read the notes first: this one works differently from the rest.

`Cursor Settings → Models`:

```text
OpenAI API Key:             ap-your-key
Override OpenAI Base URL:   http://localhost:8080/v1

Then add the models to use, for example gpt-5.
```

- Cursor sends these requests from its own servers, not from your machine. The gateway has to be reachable at a public HTTPS address; one on localhost will not work.
- For the same reason your prompts pass through Cursor before they reach Mutegate: the gateway guards the step from Cursor to the model provider, not the one from your machine to Cursor.
- Only chat and agent use the custom key. Tab completion and Cursor's own models do not go through it.
- Claude works here too: the gateway translates the OpenAI chat API into Anthropic's, tool calls and images included. Add the model under its Claude name, for example claude-sonnet-4-5.
- Cursor cannot send custom headers, so incidents are attributed to the key alone: give Cursor a key of its own.

[Cursor documentation](https://cursor.com/help/models-and-usage/api-keys)

### Cline

*OpenAI Chat Completions · /v1/chat/completions*

In Cline's settings, choose the OpenAI Compatible provider and point it at the gateway.

`Cline settings → API Provider: OpenAI Compatible`:

```text
Base URL:         http://localhost:8080/v1
API Key:          ap-your-key
Model ID:         gpt-5
Custom Headers:   X-Mutegate-Agent = cline
```

- For Claude, use Cline's Anthropic provider instead: tick "Use custom base URL" and enter the gateway's address without /v1. It has no custom headers.
- Cline runs on your machine, so a gateway on localhost works.

[Cline documentation](https://docs.cline.bot/provider-config/openai-compatible)

### Continue

*OpenAI Chat Completions · /v1/chat/completions*

Add the gateway as a model in Continue's config: once as OpenAI, once as Anthropic for Claude.

`~/.continue/config.yaml`:

```yaml
name: Mutegate
version: 0.0.1
schema: v1
models:
  - name: GPT via Mutegate
    provider: openai
    model: gpt-5
    apiBase: http://localhost:8080/v1
    apiKey: ap-your-key
    roles: [chat, edit, apply]
    requestOptions:
      headers:
        X-Mutegate-Agent: continue
  - name: Claude via Mutegate
    provider: anthropic
    model: claude-sonnet-4-5
    apiBase: http://localhost:8080/v1/
    apiKey: ap-your-key
    roles: [chat, edit, apply]
    requestOptions:
      headers:
        X-Mutegate-Agent: continue
```

- Do not give these models the embed, rerank or autocomplete roles: the gateway serves no embeddings or legacy completions yet, so those would fail. Keep Continue's local embedder.
- For o-series and GPT-5 models Continue uses the Responses API, which the gateway serves too.

[Continue documentation](https://docs.continue.dev/customize/model-providers/top-level/openai)

### Aider

*OpenAI Chat Completions · /v1/chat/completions*

Aider reads the base URL and key from the environment. Name models with their provider in front.

OpenAI models:

```bash
export OPENAI_API_BASE=http://localhost:8080/v1
export OPENAI_API_KEY=ap-your-key
aider --model openai/gpt-5 --weak-model openai/gpt-5-mini
```

Claude:

```bash
export ANTHROPIC_API_BASE=http://localhost:8080
export ANTHROPIC_API_KEY=ap-your-key
aider --model anthropic/claude-sonnet-4-5 --weak-model anthropic/claude-haiku-4-5
```

To name the agent — `.aider.model.settings.yml`:

```yaml
- name: aider/extra_params
  extra_params:
    extra_headers:
      X-Mutegate-Agent: aider
```

- Aider also calls a weak model for commit messages and summaries. Point it at a model the gateway routes, as above, or those calls go around it.
- With the Mutegate CLI (github.com/Mutegate/cli), mutegate run -- aider --model openai/gpt-5 sets the variables for that run only.

[Aider documentation](https://aider.chat/docs/llms/openai-compat.html)

### OpenCode

*OpenAI Chat Completions · /v1/chat/completions*

Add the gateway as an OpenAI-compatible provider, and point the built-in Anthropic provider at it for Claude.

`~/.config/opencode/opencode.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "mutegate": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Mutegate",
      "options": {
        "baseURL": "http://localhost:8080/v1",
        "apiKey": "{env:MUTEGATE_API_KEY}",
        "headers": { "X-Mutegate-Agent": "opencode" }
      },
      "models": { "gpt-5": { "name": "GPT-5 via Mutegate" } }
    },
    "anthropic": {
      "options": {
        "baseURL": "http://localhost:8080/v1",
        "headers": { "X-Mutegate-Agent": "opencode" }
      }
    }
  }
}
```

Then:

```bash
export MUTEGATE_API_KEY=ap-your-key
export ANTHROPIC_API_KEY=ap-your-key
opencode
```

- List under "models" the ones your organization's providers serve, Claude included: the gateway translates the OpenAI chat API into Anthropic's, tool calls and images too. The anthropic entry talks to Claude natively, which keeps features only that API has, such as prompt caching.

[OpenCode documentation](https://opencode.ai/docs/providers/)

## SDKs and code

[OpenAI Python](#openai-python) · [OpenAI Node](#openai-node) · [Anthropic Python](#anthropic-python) · [Anthropic TypeScript](#anthropic-typescript) · [LangChain](#langchain) · [curl](#curl)

### OpenAI Python

*OpenAI Chat Completions · /v1/chat/completions*

Existing code needs no change: the SDK reads the base URL and key from the environment.

Without touching the code:

```bash
export OPENAI_BASE_URL=http://localhost:8080/v1
export OPENAI_API_KEY=ap-your-key
```

Or in code, naming the agent:

```python
import os
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8080/v1",
    api_key=os.environ["MUTEGATE_API_KEY"],
    default_headers={"X-Mutegate-Agent": "my-app"},
)
reply = client.chat.completions.create(
    model="gpt-5-mini",
    messages=[{"role": "user", "content": "Hello"}],
)
print(reply.choices[0].message.content)
```

- Chat completions, the Responses API and the model list work. Embeddings, images, audio and files are not served yet.

[OpenAI Python documentation](https://github.com/openai/openai-python)

### OpenAI Node

*OpenAI Chat Completions · /v1/chat/completions*

Existing code needs no change: the SDK reads the base URL and key from the environment.

Without touching the code:

```bash
export OPENAI_BASE_URL=http://localhost:8080/v1
export OPENAI_API_KEY=ap-your-key
```

Or in code, naming the agent:

```ts
import OpenAI from 'openai'

const client = new OpenAI({
  baseURL: 'http://localhost:8080/v1',
  apiKey: process.env.MUTEGATE_API_KEY,
  defaultHeaders: { 'X-Mutegate-Agent': 'my-app' },
})
const reply = await client.chat.completions.create({
  model: 'gpt-5-mini',
  messages: [{ role: 'user', content: 'Hello' }],
})
console.log(reply.choices[0].message.content)
```

- Chat completions, the Responses API and the model list work. Embeddings, images, audio and files are not served yet.

[OpenAI Node documentation](https://github.com/openai/openai-node)

### Anthropic Python

*Anthropic Messages API · /v1/messages*

The gateway serves the Messages API natively. Note the base URL has no /v1: the SDK adds it.

Without touching the code:

```bash
export ANTHROPIC_BASE_URL=http://localhost:8080
export ANTHROPIC_API_KEY=ap-your-key
```

Or in code, naming the agent:

```python
import os
from anthropic import Anthropic

client = Anthropic(
    base_url="http://localhost:8080",
    api_key=os.environ["MUTEGATE_API_KEY"],
    default_headers={"X-Mutegate-Agent": "my-app"},
)
message = client.messages.create(
    model="claude-sonnet-4-5",
    max_tokens=1024,
    messages=[{"role": "user", "content": "Hello"}],
)
print(message.content[0].text)
```

- messages.create works, streaming and tools included. Token counting, batches, files and models.list are not served.

[Anthropic Python documentation](https://platform.claude.com/docs/en/api/sdks/python)

### Anthropic TypeScript

*Anthropic Messages API · /v1/messages*

The gateway serves the Messages API natively. Note the base URL has no /v1: the SDK adds it.

Without touching the code:

```bash
export ANTHROPIC_BASE_URL=http://localhost:8080
export ANTHROPIC_API_KEY=ap-your-key
```

Or in code, naming the agent:

```ts
import Anthropic from '@anthropic-ai/sdk'

const client = new Anthropic({
  baseURL: 'http://localhost:8080',
  apiKey: process.env.MUTEGATE_API_KEY,
  defaultHeaders: { 'X-Mutegate-Agent': 'my-app' },
})
const message = await client.messages.create({
  model: 'claude-sonnet-4-5',
  max_tokens: 1024,
  messages: [{ role: 'user', content: 'Hello' }],
})
```

- messages.create works, streaming and tools included. Token counting, batches, files and models.list are not served.

[Anthropic TypeScript documentation](https://platform.claude.com/docs/en/api/sdks/typescript)

### LangChain

*OpenAI Chat Completions · /v1/chat/completions*

Both chat models take a base URL and headers; Claude goes through ChatAnthropic, natively.

```python
import os
from langchain_openai import ChatOpenAI
from langchain_anthropic import ChatAnthropic

headers = {"X-Mutegate-Agent": "my-app"}
gpt = ChatOpenAI(
    model="gpt-5-mini",
    base_url="http://localhost:8080/v1",
    api_key=os.environ["MUTEGATE_API_KEY"],
    default_headers=headers,
)
claude = ChatAnthropic(
    model="claude-sonnet-4-5",
    base_url="http://localhost:8080",
    api_key=os.environ["MUTEGATE_API_KEY"],
    default_headers=headers,
)
```

- OpenAIEmbeddings and ChatAnthropic's token counting are not served yet.

[LangChain documentation](https://docs.langchain.com/oss/python/integrations/chat/openai)

### curl

*OpenAI Chat Completions · /v1/chat/completions*

The quickest check that the key works and the gateway is scanning: this request carries a fake AWS key, so it is blocked with a 403 and shows up in the incident feed.

OpenAI API:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer ap-your-key" \
  -H "Content-Type: application/json" \
  -H "X-Mutegate-Agent: curl-test" \
  -d '{"model":"gpt-5-mini","messages":[{"role":"user","content":"deploy with AKIAIOSFODNN7EXAMPLE"}]}'
```

Anthropic API:

```bash
curl http://localhost:8080/v1/messages \
  -H "x-api-key: ap-your-key" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -H "X-Mutegate-Agent: curl-test" \
  -d '{"model":"claude-sonnet-4-5","max_tokens":256,"messages":[{"role":"user","content":"deploy with AKIAIOSFODNN7EXAMPLE"}]}'
```

- Replace the fake key with a harmless sentence to see a clean request go through to the provider.

[curl documentation](https://github.com/Mutegate/mutegate/blob/main/docs/API.md)
