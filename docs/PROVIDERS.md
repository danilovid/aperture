# Providers and routing

## Which provider a request goes to

The model name decides. Custom prefixes are matched first (the longest match
wins); then the built-ins: `claude*` → Anthropic, `llama*` / `mixtral*` →
Groq, everything else → OpenAI.

`GET /v1/models` answers with what a key can actually use: every provider it
has a credential for is asked live, and a model is listed only if it would
route back to the provider that listed it. A provider that fails is named
under `unavailable` instead of failing the whole list.

## What a request costs

Spend is computed from the tokens a provider reports and a model catalog built
into the binary: prices, cache prices, the context window and what the model
does (chat, embeddings, images, speech…) for about 500 models from OpenAI,
Anthropic, Groq and the common OpenAI-compatible providers — DeepSeek, Qwen,
Kimi, GLM, Grok, Mistral, Gemini. It lives in
[`internal/pricing/catalog.json`](../internal/pricing/catalog.json), generated
from [LiteLLM's public model list](https://github.com/BerriAI/litellm) (MIT)
and committed, so pricing works without internet access. To refresh it:

```bash
go generate ./internal/pricing
```

A dated snapshot (`claude-sonnet-4-5-20250929`) costs what its name does; a
model the catalog does not know is recorded with its tokens and a cost of `0`.

Prompt caching is counted as input and priced at the cache's own rates:
Anthropic's cache reads, five-minute and one-hour writes, and the cached input
OpenAI reports on its own. That matters for coding agents — Claude Code sends
most of its prompt as cache reads, which are both the bulk of its tokens and a
tenth of their plain price.

## Providers in the console

With a database, each organization sets up its own providers in
**Settings → Providers**: the address, the key, an optional proxy, a timeout,
and a connectivity check before saving. The environment keeps two roles:

- `OPENAI_BASE_URL`, `ANTHROPIC_BASE_URL` and `JEV_BASE_URL` are the default
  addresses for every organization that has not set its own;
- `OPENAI_API_KEY` and the other provider keys, and `CUSTOM_PROVIDERS`, belong
  to the **default organization only**. They are copied into its providers on
  start when missing; after that the console is where they live.

Without a database the environment is the only source, for everybody.

## Custom OpenAI-compatible providers

Route any OpenAI-compatible endpoint — DeepSeek, Qwen/DashScope, Moonshot,
GLM, a local Ollama or vLLM, a private gateway — by model prefix.

In the console, **Settings → Providers** has presets for the common ones:
the address, the model prefixes and a link to the provider's docs are filled
in, and only the key is left to paste. A new provider's connection is checked
before it is saved; if the check fails, saving it anyway is a deliberate
second click. The presets are data —
[`web/src/console/providerPresets.json`](../web/src/console/providerPresets.json),
held by a test to the same rules a save is — so adding one is a small PR.
Anything else is a provider of kind *OpenAI-compatible*. From the environment:

```bash
export CUSTOM_PROVIDERS='[
  {"name":"deepseek","base_url":"https://api.deepseek.com/v1","prefixes":["deepseek"],"api_key":"sk-..."},
  {"name":"qwen","base_url":"https://dashscope.aliyuncs.com/compatible-mode/v1","prefixes":["qwen"],"api_key":"sk-..."},
  {"name":"ollama","base_url":"http://localhost:11434/v1","prefixes":["mistral","gemma"],"api_key":"ollama"}
]'
# then {"model":"deepseek-chat", ...} is scanned by DLP and proxied to DeepSeek
```

`base_url` must already include the version segment. Local endpoints (Ollama,
vLLM) ignore auth — set any placeholder `api_key` so the provider counts as
configured. Every request is scanned by DLP and attributed to the provider's
name in the incident feed and the statistics.

## Corporate proxies

`HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` apply to every provider without a
proxy of its own. A provider's own proxy (set in the console) carries all of
its traffic, `NO_PROXY` notwithstanding. If the proxy intercepts TLS, its CA
has to be in the system trust store; the connectivity check says so when it
is not.

## The Jev decision API

Not every leak goes to a model. [Jev](https://www.jevai.org/docs) is a
decision API: an agent posts business fields — the customer message, the tool
arguments, the policy text — and gets back a typed decision. No prompt, no
tokens, but the same egress problem. Mutegate fronts it on the same paths, so
an agent switches by changing one base URL:

```bash
export JEV_API_KEY=...            # from jevai.org/agent/keys
curl http://localhost:8080/api/v1/decisions/tool-guard \
  -H "Authorization: Bearer $MUTEGATE_API_KEY" -H "Content-Type: application/json" \
  -d '{"tool":"issue_customer_refund","action":"Refund USD 680 after a duplicate charge",
       "arguments_summary":["order_id=ord_7429","contact=alice@example.com"]}'
# → the decision comes back untouched; Jev received contact=[REDACTED:email]
```

A decision body has no message list, so the whole JSON is scanned wherever
the strings sit — including arrays of plain strings, which is where
`arguments_summary` and `evidence` live. Blocked requests never reach the API
and come back in Jev's own `{code, message, data}` envelope, so a client that
only parses Jev errors still understands what happened. Only the six
documented endpoints are proxied; anything else is a `404` from the gateway.
Jev reports no token usage, so these calls are logged and rate-limited but
cost nothing. A runnable example is [`examples/jev.sh`](../examples/jev.sh).
