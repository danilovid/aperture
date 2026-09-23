# Providers and routing

## Which provider a request goes to

The model name decides. Custom prefixes are matched first (the longest match
wins); then the built-ins: `claude*` → Anthropic, `llama*` / `mixtral*` →
Groq, everything else → OpenAI.

`GET /v1/models` answers with what a key can actually use: every provider it
has a credential for is asked live, and a model is listed only if it would
route back to the provider that listed it. A provider that fails is named
under `unavailable` instead of failing the whole list.

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
GLM, a local Ollama or vLLM, a private gateway — by model prefix. In the
console, add a provider of kind *OpenAI-compatible*; from the environment:

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
