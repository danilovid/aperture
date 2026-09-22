// The "Continue with …" buttons for the configured identity providers.
import { useState } from 'react'
import { auth } from '../api'
import type { OAuthProvider } from '../api'
import { Notice } from '../console/forms'

/** A small mark per provider, drawn rather than fetched. */
function Mark({ id }: { id: string }) {
  const color = id === 'google' ? '#4285F4' : id === 'yandex' ? '#FC3F1D' : 'var(--text)'
  const letter = id === 'google' ? 'G' : id === 'yandex' ? 'Я' : ''
  if (id === 'github') {
    return (
      <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" fill="currentColor">
        <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" />
      </svg>
    )
  }
  return (
    <span aria-hidden="true" style={{ width: 16, height: 16, borderRadius: '50%', background: color, color: '#fff', fontSize: 10.5, fontWeight: 700, display: 'inline-flex', alignItems: 'center', justifyContent: 'center', lineHeight: 1 }}>
      {letter}
    </span>
  )
}

/**
 * "Continue with …" for every configured provider, and nothing at all when
 * none is — the password form then stands alone, as it always did.
 */
export function ProviderButtons({
  providers,
  opts,
  label = 'Continue with',
  divider = true,
}: {
  providers: OAuthProvider[]
  opts: { next?: string; invite?: string; link?: boolean }
  label?: string
  divider?: boolean
}) {
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState('')
  if (providers.length === 0) return null

  const go = async (id: string) => {
    setBusy(id)
    setError('')
    try {
      const { url } = await auth.oauthStart(id, opts)
      window.location.assign(url)
    } catch (e) {
      setError((e as Error).message)
      setBusy(null)
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {providers.map((p) => (
        <button
          key={p.id}
          type="button"
          onClick={() => go(p.id)}
          disabled={busy !== null}
          className="ap-save-btn"
          style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 9, background: 'var(--bg3)', color: 'var(--text)', border: '1px solid var(--border2)', padding: '9px 16px', borderRadius: 7, fontSize: 13.5, fontWeight: 600, cursor: busy ? 'default' : 'pointer', opacity: busy && busy !== p.id ? 0.6 : 1 }}
        >
          <Mark id={p.id} />
          {busy === p.id ? `Going to ${p.name}…` : `${label} ${p.name}`}
        </button>
      ))}
      {error && <Notice>{error}</Notice>}
      {divider && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, color: 'var(--faint)', fontSize: 12, margin: '6px 0 2px' }}>
          <span style={{ flex: 1, height: 1, background: 'var(--border)' }} />
          or with a password
          <span style={{ flex: 1, height: 1, background: 'var(--border)' }} />
        </div>
      )}
    </div>
  )
}
