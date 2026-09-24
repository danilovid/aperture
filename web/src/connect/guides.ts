// How to point each tool at the gateway. One list, three readers: the public
// /connect page, the console's API keys screen (with a real key filled in),
// and docs/CONNECT.md, which `npm run docs:connect` writes from it — so the
// three cannot drift apart. Plain data and one function: Node reads this file
// too, with its types stripped.

export type Lang = 'bash' | 'toml' | 'json' | 'yaml' | 'python' | 'ts' | 'text'

export interface Snippet {
  /** What this step is, when a guide has more than one. */
  label?: string
  lang: Lang
  /** The file it goes in, when it is a file. */
  file?: string
  /** With {{base}}, {{key}} and {{agent}} to fill in. */
  code: string
}

export interface Guide {
  /** The URL slug, and — unless agent says otherwise — the agent name the snippets attribute traffic to. */
  id: string
  /** The X-Mutegate-Agent the snippets send, when the tool's id is not it. */
  agent?: string
  name: string
  group: 'agent' | 'sdk'
  /** Which of the gateway's APIs it speaks. */
  api: 'anthropic' | 'openai' | 'responses'
  intro: string
  snippets: Snippet[]
  /** What works, what does not, and what to know. */
  notes: string[]
  docs: string
}

/** What stands in for a key nobody has pasted yet. */
export const KEY_PLACEHOLDER = 'ap-your-key'

/** How each of the gateway's APIs is named, and where it is served. */
export const apiName: Record<Guide['api'], string> = {
  anthropic: 'Anthropic Messages API · /v1/messages',
  openai: 'OpenAI Chat Completions · /v1/chat/completions',
  responses: 'OpenAI Responses API · /v1/responses',
}

export interface Fill {
  /** The gateway's address, without a trailing slash. */
  base: string
  key: string
  agent: string
}

export function fill(code: string, f: Fill): string {
  return code.replaceAll('{{base}}', f.base).replaceAll('{{key}}', f.key).replaceAll('{{agent}}', f.agent)
}

/** What the gateway does to the traffic, in the order a request meets it. */
export const explainer: { title: string; body: string }[] = [
  {
    title: 'Every request is read on the way out',
    body: 'Prompts, the system prompt, tool-call arguments and tool results — where a file the agent just read ends up — are scanned before anything reaches the provider. Clean traffic passes through untouched, streaming included.',
  },
  {
    title: 'Personal data is redacted in place',
    body: 'The provider receives [REDACTED:email] instead of the address. The tool notices nothing: the request goes through, and the answer comes back as usual.',
  },
  {
    title: 'Secrets are blocked',
    body: 'The request never reaches the provider, and the tool gets an HTTP 403 saying which rule stopped it — on the Anthropic API a permission_error, on the OpenAI APIs an error of type mutegate_dlp_blocked. Most tools show it as an API error.',
  },
  {
    title: 'Tell agents apart',
    body: 'Several agents usually share a key. Where a tool can send headers, X-Mutegate-Agent names the agent and X-Mutegate-Session the run, and both show up on incidents and on the cost figures.',
  },
]

/** The agent name a guide's snippets send. */
export function agentOf(g: Guide): string {
  return g.agent ?? g.id
}

const openaiSDKNotes = [
  'Chat completions, the Responses API and the model list work. Embeddings, images, audio and files are not served yet.',
]

export const guides: Guide[] = [
  {
    id: 'claude-code',
    name: 'Claude Code',
    group: 'agent',
    api: 'anthropic',
    intro: 'Claude Code speaks the Anthropic Messages API, which the gateway serves natively. Three environment variables, no code change.',
    snippets: [
      {
        label: 'In the shell',
        lang: 'bash',
        code: `export ANTHROPIC_BASE_URL={{base}}
export ANTHROPIC_AUTH_TOKEN={{key}}
export ANTHROPIC_CUSTOM_HEADERS='X-Mutegate-Agent: {{agent}}'
claude`,
      },
      {
        label: 'Or for good',
        lang: 'json',
        file: '~/.claude/settings.json',
        code: `{
  "env": {
    "ANTHROPIC_BASE_URL": "{{base}}",
    "ANTHROPIC_AUTH_TOKEN": "{{key}}",
    "ANTHROPIC_CUSTOM_HEADERS": "X-Mutegate-Agent: {{agent}}"
  }
}`,
      },
    ],
    notes: [
      'ANTHROPIC_AUTH_TOKEN is sent as a Bearer token and needs no approval; ANTHROPIC_API_KEY works too, as x-api-key, and asks once.',
      'Keep the key in ~/.claude/settings.json or .claude/settings.local.json, never in the .claude/settings.json a repository commits. In the VS Code extension the same variables go in claudeCode.environmentVariables.',
      'A blocked request shows as "API Error: 403 request blocked by DLP policy: sensitive data detected (…)". Claude Code puts "Failed to authenticate" in front of it; the rule named at the end is the real reason.',
      'Token counting (/v1/messages/count_tokens) is not served. Claude Code falls back to an estimate, so /context shows approximate numbers; nothing else changes.',
      'With the Mutegate CLI (github.com/danilovid/mutegate-cli): mutegate login, then mutegate run -- claude. Nothing is written anywhere, and every run is its own session on incidents and costs.',
    ],
    docs: 'https://code.claude.com/docs/en/llm-gateway-connect',
  },
  {
    id: 'codex',
    name: 'Codex CLI',
    group: 'agent',
    api: 'responses',
    intro: 'Codex speaks the OpenAI Responses API, which the gateway serves at /v1/responses. Add the gateway as a model provider and make it the default.',
    snippets: [
      {
        lang: 'toml',
        file: '~/.codex/config.toml',
        code: `model = "gpt-5"
model_provider = "mutegate"

[model_providers.mutegate]
name = "Mutegate"
base_url = "{{base}}/v1"
env_key = "MUTEGATE_API_KEY"
wire_api = "responses"
http_headers = { "X-Mutegate-Agent" = "{{agent}}" }`,
      },
      {
        label: 'Then',
        lang: 'bash',
        code: `export MUTEGATE_API_KEY={{key}}
codex`,
      },
    ],
    notes: [
      'The provider has to be defined in ~/.codex/config.toml: a project\'s .codex/config.toml cannot change model_provider.',
      'env_key names the variable Codex reads the key from, and sends as a Bearer token.',
      'With the Mutegate CLI (github.com/danilovid/mutegate-cli), mutegate connect codex writes this file for you, and mutegate run -- codex starts it with the key and a session of its own.',
    ],
    docs: 'https://developers.openai.com/codex/config-advanced',
  },
  {
    id: 'cursor',
    name: 'Cursor',
    group: 'agent',
    api: 'openai',
    intro: 'Cursor takes an OpenAI key and a base URL override in its settings. Read the notes first: this one works differently from the rest.',
    snippets: [
      {
        lang: 'text',
        file: 'Cursor Settings → Models',
        code: `OpenAI API Key:             {{key}}
Override OpenAI Base URL:   {{base}}/v1

Then add the models to use, for example gpt-5.`,
      },
    ],
    notes: [
      'Cursor sends these requests from its own servers, not from your machine. The gateway has to be reachable at a public HTTPS address; one on localhost will not work.',
      'For the same reason your prompts pass through Cursor before they reach Mutegate: the gateway guards the step from Cursor to the model provider, not the one from your machine to Cursor.',
      'Only chat and agent use the custom key. Tab completion and Cursor\'s own models do not go through it.',
      'Use OpenAI or other OpenAI-compatible models here. Claude through the OpenAI chat API is text-only in this gateway for now — no tool calls — which the agent needs.',
      'Cursor cannot send custom headers, so incidents are attributed to the key alone: give Cursor a key of its own.',
    ],
    docs: 'https://cursor.com/help/models-and-usage/api-keys',
  },
  {
    id: 'cline',
    name: 'Cline',
    group: 'agent',
    api: 'openai',
    intro: 'In Cline\'s settings, choose the OpenAI Compatible provider and point it at the gateway.',
    snippets: [
      {
        lang: 'text',
        file: 'Cline settings → API Provider: OpenAI Compatible',
        code: `Base URL:         {{base}}/v1
API Key:          {{key}}
Model ID:         gpt-5
Custom Headers:   X-Mutegate-Agent = {{agent}}`,
      },
    ],
    notes: [
      'For Claude, use Cline\'s Anthropic provider instead: tick "Use custom base URL" and enter the gateway\'s address without /v1. It has no custom headers.',
      'Cline runs on your machine, so a gateway on localhost works.',
    ],
    docs: 'https://docs.cline.bot/provider-config/openai-compatible',
  },
  {
    id: 'continue',
    name: 'Continue',
    group: 'agent',
    api: 'openai',
    intro: 'Add the gateway as a model in Continue\'s config: once as OpenAI, once as Anthropic for Claude.',
    snippets: [
      {
        lang: 'yaml',
        file: '~/.continue/config.yaml',
        code: `name: Mutegate
version: 0.0.1
schema: v1
models:
  - name: GPT via Mutegate
    provider: openai
    model: gpt-5
    apiBase: {{base}}/v1
    apiKey: {{key}}
    roles: [chat, edit, apply]
    requestOptions:
      headers:
        X-Mutegate-Agent: {{agent}}
  - name: Claude via Mutegate
    provider: anthropic
    model: claude-sonnet-4-5
    apiBase: {{base}}/v1/
    apiKey: {{key}}
    roles: [chat, edit, apply]
    requestOptions:
      headers:
        X-Mutegate-Agent: {{agent}}`,
      },
    ],
    notes: [
      'Do not give these models the embed, rerank or autocomplete roles: the gateway serves no embeddings or legacy completions yet, so those would fail. Keep Continue\'s local embedder.',
      'For o-series and GPT-5 models Continue uses the Responses API, which the gateway serves too.',
    ],
    docs: 'https://docs.continue.dev/customize/model-providers/top-level/openai',
  },
  {
    id: 'aider',
    name: 'Aider',
    group: 'agent',
    api: 'openai',
    intro: 'Aider reads the base URL and key from the environment. Name models with their provider in front.',
    snippets: [
      {
        label: 'OpenAI models',
        lang: 'bash',
        code: `export OPENAI_API_BASE={{base}}/v1
export OPENAI_API_KEY={{key}}
aider --model openai/gpt-5 --weak-model openai/gpt-5-mini`,
      },
      {
        label: 'Claude',
        lang: 'bash',
        code: `export ANTHROPIC_API_BASE={{base}}
export ANTHROPIC_API_KEY={{key}}
aider --model anthropic/claude-sonnet-4-5 --weak-model anthropic/claude-haiku-4-5`,
      },
      {
        label: 'To name the agent',
        lang: 'yaml',
        file: '.aider.model.settings.yml',
        code: `- name: aider/extra_params
  extra_params:
    extra_headers:
      X-Mutegate-Agent: {{agent}}`,
      },
    ],
    notes: [
      'Aider also calls a weak model for commit messages and summaries. Point it at a model the gateway routes, as above, or those calls go around it.',
      'With the Mutegate CLI (github.com/danilovid/mutegate-cli), mutegate run -- aider --model openai/gpt-5 sets the variables for that run only.',
    ],
    docs: 'https://aider.chat/docs/llms/openai-compat.html',
  },
  {
    id: 'opencode',
    name: 'OpenCode',
    group: 'agent',
    api: 'openai',
    intro: 'Add the gateway as an OpenAI-compatible provider, and point the built-in Anthropic provider at it for Claude.',
    snippets: [
      {
        lang: 'json',
        file: '~/.config/opencode/opencode.json',
        code: `{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "mutegate": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Mutegate",
      "options": {
        "baseURL": "{{base}}/v1",
        "apiKey": "{env:MUTEGATE_API_KEY}",
        "headers": { "X-Mutegate-Agent": "{{agent}}" }
      },
      "models": { "gpt-5": { "name": "GPT-5 via Mutegate" } }
    },
    "anthropic": {
      "options": {
        "baseURL": "{{base}}/v1",
        "headers": { "X-Mutegate-Agent": "{{agent}}" }
      }
    }
  }
}`,
      },
      {
        label: 'Then',
        lang: 'bash',
        code: `export MUTEGATE_API_KEY={{key}}
export ANTHROPIC_API_KEY={{key}}
opencode`,
      },
    ],
    notes: [
      'List under "models" the ones your organization\'s providers serve. Claude goes through the anthropic entry: through the OpenAI-compatible one it would be text-only, without tools.',
    ],
    docs: 'https://opencode.ai/docs/providers/',
  },
  {
    id: 'openai-python',
    name: 'OpenAI Python',
    group: 'sdk',
    api: 'openai',
    agent: 'my-app',
    intro: 'Existing code needs no change: the SDK reads the base URL and key from the environment.',
    snippets: [
      {
        label: 'Without touching the code',
        lang: 'bash',
        code: `export OPENAI_BASE_URL={{base}}/v1
export OPENAI_API_KEY={{key}}`,
      },
      {
        label: 'Or in code, naming the agent',
        lang: 'python',
        code: `import os
from openai import OpenAI

client = OpenAI(
    base_url="{{base}}/v1",
    api_key=os.environ["MUTEGATE_API_KEY"],
    default_headers={"X-Mutegate-Agent": "{{agent}}"},
)
reply = client.chat.completions.create(
    model="gpt-5-mini",
    messages=[{"role": "user", "content": "Hello"}],
)
print(reply.choices[0].message.content)`,
      },
    ],
    notes: openaiSDKNotes,
    docs: 'https://github.com/openai/openai-python',
  },
  {
    id: 'openai-node',
    name: 'OpenAI Node',
    group: 'sdk',
    api: 'openai',
    agent: 'my-app',
    intro: 'Existing code needs no change: the SDK reads the base URL and key from the environment.',
    snippets: [
      {
        label: 'Without touching the code',
        lang: 'bash',
        code: `export OPENAI_BASE_URL={{base}}/v1
export OPENAI_API_KEY={{key}}`,
      },
      {
        label: 'Or in code, naming the agent',
        lang: 'ts',
        code: `import OpenAI from 'openai'

const client = new OpenAI({
  baseURL: '{{base}}/v1',
  apiKey: process.env.MUTEGATE_API_KEY,
  defaultHeaders: { 'X-Mutegate-Agent': '{{agent}}' },
})
const reply = await client.chat.completions.create({
  model: 'gpt-5-mini',
  messages: [{ role: 'user', content: 'Hello' }],
})
console.log(reply.choices[0].message.content)`,
      },
    ],
    notes: openaiSDKNotes,
    docs: 'https://github.com/openai/openai-node',
  },
  {
    id: 'anthropic-python',
    name: 'Anthropic Python',
    group: 'sdk',
    api: 'anthropic',
    agent: 'my-app',
    intro: 'The gateway serves the Messages API natively. Note the base URL has no /v1: the SDK adds it.',
    snippets: [
      {
        label: 'Without touching the code',
        lang: 'bash',
        code: `export ANTHROPIC_BASE_URL={{base}}
export ANTHROPIC_API_KEY={{key}}`,
      },
      {
        label: 'Or in code, naming the agent',
        lang: 'python',
        code: `import os
from anthropic import Anthropic

client = Anthropic(
    base_url="{{base}}",
    api_key=os.environ["MUTEGATE_API_KEY"],
    default_headers={"X-Mutegate-Agent": "{{agent}}"},
)
message = client.messages.create(
    model="claude-sonnet-4-5",
    max_tokens=1024,
    messages=[{"role": "user", "content": "Hello"}],
)
print(message.content[0].text)`,
      },
    ],
    notes: ['messages.create works, streaming and tools included. Token counting, batches, files and models.list are not served.'],
    docs: 'https://platform.claude.com/docs/en/api/sdks/python',
  },
  {
    id: 'anthropic-typescript',
    name: 'Anthropic TypeScript',
    group: 'sdk',
    api: 'anthropic',
    agent: 'my-app',
    intro: 'The gateway serves the Messages API natively. Note the base URL has no /v1: the SDK adds it.',
    snippets: [
      {
        label: 'Without touching the code',
        lang: 'bash',
        code: `export ANTHROPIC_BASE_URL={{base}}
export ANTHROPIC_API_KEY={{key}}`,
      },
      {
        label: 'Or in code, naming the agent',
        lang: 'ts',
        code: `import Anthropic from '@anthropic-ai/sdk'

const client = new Anthropic({
  baseURL: '{{base}}',
  apiKey: process.env.MUTEGATE_API_KEY,
  defaultHeaders: { 'X-Mutegate-Agent': '{{agent}}' },
})
const message = await client.messages.create({
  model: 'claude-sonnet-4-5',
  max_tokens: 1024,
  messages: [{ role: 'user', content: 'Hello' }],
})`,
      },
    ],
    notes: ['messages.create works, streaming and tools included. Token counting, batches, files and models.list are not served.'],
    docs: 'https://platform.claude.com/docs/en/api/sdks/typescript',
  },
  {
    id: 'langchain',
    name: 'LangChain',
    group: 'sdk',
    api: 'openai',
    agent: 'my-app',
    intro: 'Both chat models take a base URL and headers; Claude goes through ChatAnthropic, natively.',
    snippets: [
      {
        lang: 'python',
        code: `import os
from langchain_openai import ChatOpenAI
from langchain_anthropic import ChatAnthropic

headers = {"X-Mutegate-Agent": "{{agent}}"}
gpt = ChatOpenAI(
    model="gpt-5-mini",
    base_url="{{base}}/v1",
    api_key=os.environ["MUTEGATE_API_KEY"],
    default_headers=headers,
)
claude = ChatAnthropic(
    model="claude-sonnet-4-5",
    base_url="{{base}}",
    api_key=os.environ["MUTEGATE_API_KEY"],
    default_headers=headers,
)`,
      },
    ],
    notes: ['OpenAIEmbeddings and ChatAnthropic\'s token counting are not served yet.'],
    docs: 'https://docs.langchain.com/oss/python/integrations/chat/openai',
  },
  {
    id: 'curl',
    name: 'curl',
    group: 'sdk',
    api: 'openai',
    agent: 'curl-test',
    intro: 'The quickest check that the key works and the gateway is scanning: this request carries a fake AWS key, so it is blocked with a 403 and shows up in the incident feed.',
    snippets: [
      {
        label: 'OpenAI API',
        lang: 'bash',
        code: `curl {{base}}/v1/chat/completions \\
  -H "Authorization: Bearer {{key}}" \\
  -H "Content-Type: application/json" \\
  -H "X-Mutegate-Agent: {{agent}}" \\
  -d '{"model":"gpt-5-mini","messages":[{"role":"user","content":"deploy with AKIAIOSFODNN7EXAMPLE"}]}'`,
      },
      {
        label: 'Anthropic API',
        lang: 'bash',
        code: `curl {{base}}/v1/messages \\
  -H "x-api-key: {{key}}" \\
  -H "anthropic-version: 2023-06-01" \\
  -H "Content-Type: application/json" \\
  -H "X-Mutegate-Agent: {{agent}}" \\
  -d '{"model":"claude-sonnet-4-5","max_tokens":256,"messages":[{"role":"user","content":"deploy with AKIAIOSFODNN7EXAMPLE"}]}'`,
      },
    ],
    notes: ['Replace the fake key with a harmless sentence to see a clean request go through to the provider.'],
    docs: 'https://github.com/danilovid/mutegate/blob/main/docs/API.md',
  },
]
