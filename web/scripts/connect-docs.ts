// Writes docs/CONNECT.md from src/connect/guides.ts, the list the console and
// the /connect page read, so the repository's guide says what they say.
//
//   npm run docs:connect            rewrite it
//   npm run docs:connect -- --check fail if it is out of date (CI)
import { readFileSync, writeFileSync } from 'node:fs'
import { KEY_PLACEHOLDER, agentOf, apiName, explainer, fill, guides } from '../src/connect/guides.ts'
import type { Guide } from '../src/connect/guides.ts'

const target = new URL('../../docs/CONNECT.md', import.meta.url)
const base = 'http://localhost:8080'

const groups: { id: Guide['group']; title: string }[] = [
  { id: 'agent', title: 'Coding agents' },
  { id: 'sdk', title: 'SDKs and code' },
]

function anchor(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9 -]/g, '').replace(/ /g, '-')
}

function render(): string {
  const out: string[] = []
  out.push('# Connecting your tools', '')
  out.push('<!-- Generated from web/src/connect/guides.ts by `npm run docs:connect`. Edit that file, not this one. -->', '')
  out.push(
    'Point the tool at the gateway instead of the provider, with a Mutegate key instead of the',
    "provider's; the provider's own key stays on the gateway. The snippets below use",
    `\`${base}\` for the gateway and \`${KEY_PLACEHOLDER}\` for the key. Every installation serves`,
    'the same guides at `/connect`, and the console shows them under Settings → API keys with',
    'its own address and your key filled in.',
    '',
  )
  out.push('## What happens to the traffic', '')
  for (const e of explainer) out.push(`**${e.title}.** ${e.body}`, '')

  for (const g of groups) {
    const list = guides.filter((x) => x.group === g.id)
    out.push(`## ${g.title}`, '')
    out.push(list.map((x) => `[${x.name}](#${anchor(x.name)})`).join(' · '), '')
    for (const x of list) {
      const values = { base, key: KEY_PLACEHOLDER, agent: agentOf(x) }
      out.push(`### ${x.name}`, '', `*${apiName[x.api]}*`, '', x.intro, '')
      for (const s of x.snippets) {
        const head = [s.label, s.file && `\`${s.file}\``].filter(Boolean).join(' — ')
        if (head) out.push(`${head}:`, '')
        out.push('```' + s.lang, fill(s.code, values), '```', '')
      }
      for (const n of x.notes) out.push(`- ${n}`)
      if (x.notes.length) out.push('')
      out.push(`[${x.name} documentation](${x.docs})`, '')
    }
  }
  return out.join('\n').replace(/\n+$/, '\n')
}

const md = render()
if (process.argv.includes('--check')) {
  let current = ''
  try {
    current = readFileSync(target, 'utf8')
  } catch {
    // missing counts as out of date
  }
  if (current !== md) {
    console.error('docs/CONNECT.md is out of date with src/connect/guides.ts: run `npm run docs:connect` in web/.')
    process.exit(1)
  }
} else {
  writeFileSync(target, md)
}
