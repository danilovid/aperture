// A tool picker and its setup, filled in with this installation's address
// and, where there is one to show, the key.
import { useState } from 'react'
import { mono } from '../console/styles'
import { KEY_PLACEHOLDER, agentOf, apiName, fill, guides } from './guides'
import type { Guide, Snippet } from './guides'

const groups: { id: Guide['group']; label: string }[] = [
  { id: 'agent', label: 'Coding agents' },
  { id: 'sdk', label: 'SDKs and code' },
]

export function ConnectGuide({
  base,
  apiKey,
  tool,
  onTool,
}: {
  base: string
  /** The key to fill in; without one, a placeholder the reader replaces. */
  apiKey?: string
  /** The selected tool, when the caller keeps it (the public page, in the URL). */
  tool?: string
  onTool?: (id: string) => void
}) {
  const [own, setOwn] = useState(guides[0].id)
  const g = guides.find((x) => x.id === (tool ?? own)) ?? guides[0]
  const choose = (id: string) => (onTool ? onTool(id) : setOwn(id))
  const values = { base, key: apiKey ?? KEY_PLACEHOLDER, agent: agentOf(g) }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      {groups.map((grp) => (
        <div key={grp.id} style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
          <span style={{ fontSize: 12, color: 'var(--faint)', width: 110, flexShrink: 0 }}>{grp.label}</span>
          <div role="tablist" aria-label={grp.label} style={{ display: 'flex', gap: 6, flexWrap: 'wrap', flex: 1 }}>
            {guides
              .filter((x) => x.group === grp.id)
              .map((x) => {
                const on = x.id === g.id
                return (
                  <button
                    key={x.id}
                    role="tab"
                    aria-selected={on}
                    onClick={() => choose(x.id)}
                    style={{
                      padding: '5px 11px',
                      borderRadius: 99,
                      fontSize: 12.5,
                      fontWeight: 600,
                      cursor: 'pointer',
                      border: `1px solid ${on ? 'var(--accent)' : 'var(--border2)'}`,
                      background: on ? 'var(--accent-dim)' : 'transparent',
                      color: on ? 'var(--accent)' : 'var(--muted)',
                    }}
                  >
                    {x.name}
                  </button>
                )
              })}
          </div>
        </div>
      ))}

      <div role="tabpanel" aria-label={g.name} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <div style={{ ...mono, fontSize: 11.5, color: 'var(--faint)' }}>{apiName[g.api]}</div>
        <div style={{ fontSize: 13.5, color: 'var(--muted)', lineHeight: 1.55, marginTop: -6 }}>{g.intro}</div>
        {g.snippets.map((s, i) => (
          <SnippetBlock key={`${g.id}-${i}`} snippet={s} code={fill(s.code, values)} />
        ))}
        {g.notes.length > 0 && (
          <ul style={{ margin: 0, paddingLeft: 18, display: 'flex', flexDirection: 'column', gap: 6, fontSize: 13, color: 'var(--muted)', lineHeight: 1.5 }}>
            {g.notes.map((n) => (
              <li key={n}>{n}</li>
            ))}
          </ul>
        )}
        <a href={g.docs} target="_blank" rel="noreferrer" style={{ fontSize: 13, color: 'var(--accent)' }}>
          {g.name} documentation ↗
        </a>
      </div>
    </div>
  )
}

function SnippetBlock({ snippet, code }: { snippet: Snippet; code: string }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(code)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // Clipboard access can be refused; the text is there to select by hand.
    }
  }
  return (
    <div>
      {(snippet.label || snippet.file) && (
        <div style={{ display: 'flex', gap: 8, alignItems: 'baseline', fontSize: 12.5, marginBottom: 6 }}>
          {snippet.label && <span style={{ fontWeight: 600 }}>{snippet.label}</span>}
          {snippet.file && <span style={{ ...mono, color: 'var(--faint)', fontSize: 12 }}>{snippet.file}</span>}
        </div>
      )}
      <div style={{ position: 'relative' }}>
        <pre
          style={{
            ...mono,
            margin: 0,
            background: 'var(--bg)',
            border: '1px solid var(--border)',
            borderRadius: 8,
            padding: '12px 14px',
            paddingRight: 72,
            fontSize: 12.5,
            lineHeight: 1.6,
            overflowX: 'auto',
            whiteSpace: 'pre',
          }}
        >
          {code}
        </pre>
        <button
          onClick={copy}
          style={{
            position: 'absolute',
            top: 8,
            right: 8,
            padding: '3px 9px',
            borderRadius: 6,
            fontSize: 11.5,
            fontWeight: 600,
            cursor: 'pointer',
            border: '1px solid var(--border2)',
            background: 'var(--bg3)',
            color: copied ? 'var(--green)' : 'var(--muted)',
          }}
        >
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
    </div>
  )
}
