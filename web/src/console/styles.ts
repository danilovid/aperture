// Style constants and palette helpers ported from the design system.
import type { CSSProperties } from 'react'

export const mono: CSSProperties = { fontFamily: "'IBM Plex Mono', monospace" }

export const card: CSSProperties = {
  background: 'var(--bg2)',
  border: '1px solid var(--border)',
  borderRadius: 12,
}

export const h1Style: CSSProperties = { fontSize: 21, fontWeight: 700, margin: 0, letterSpacing: '-0.3px' }
export const subStyle: CSSProperties = { color: 'var(--muted)', fontSize: 13, marginTop: 2 }
export const colHead: CSSProperties = {
  fontSize: 11.5,
  color: 'var(--faint)',
  fontWeight: 600,
  letterSpacing: 0.6,
  textTransform: 'uppercase',
}

export function actionStyle(action: string): { bg: string; fg: string; label: string } {
  if (action === 'blocked' || action === 'block')
    return { bg: 'var(--red-bg)', fg: 'var(--red)', label: 'BLOCKED' }
  if (action === 'redacted' || action === 'redact')
    return { bg: 'var(--amber-bg)', fg: 'var(--amber)', label: 'REDACTED' }
  return { bg: 'var(--accent-dim)', fg: 'var(--accent)', label: 'ALERT' }
}

export function provStyle(provider: string): { bg: string; fg: string } {
  const p = provider.toLowerCase()
  if (p === 'openai') return { bg: 'var(--green-bg)', fg: 'var(--green)' }
  if (p === 'anthropic') return { bg: 'var(--amber-bg)', fg: 'var(--amber)' }
  return { bg: 'var(--accent-dim)', fg: 'var(--accent)' }
}
