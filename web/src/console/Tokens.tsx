// Service tokens: how CI and scripts reach the admin API without a person's
// session. A token belongs to this organization and can do only what its
// scopes say — the screen leads with the scopes for that reason.
import { useCallback, useEffect, useState } from 'react'
import { people, API_URL } from '../api'
import type { CreatedToken, Scope, ServiceToken } from '../api'
import { card, colHead, h1Style, mono, subStyle } from './styles'
import { Badge } from './ui'
import { Button, Notice, Select, TextInput } from './forms'
import { fmtTs, timeAgo } from './format'

const SCOPES: { value: Scope; what: string }[] = [
  { value: 'events:read', what: 'read the incident feed, reports, statistics and policies; test a policy' },
  { value: 'keys:read', what: 'list Mutegate keys' },
  { value: 'keys:write', what: 'create and delete Mutegate keys, set provider credentials' },
  { value: 'policies:write', what: 'change policies, limits and muted rules' },
]

const LIFETIMES = [
  { value: '720h', label: '30 days' },
  { value: '2160h', label: '90 days' },
  { value: '8760h', label: '1 year' },
]

function ScopeBadge({ scope }: { scope: Scope }) {
  const writes = scope.endsWith(':write')
  return (
    <Badge bg={writes ? 'var(--amber-bg)' : 'var(--bg4)'} fg={writes ? 'var(--amber)' : 'var(--muted)'}>
      {scope}
    </Badge>
  )
}

export function Tokens({ toast }: { toast: (msg: string) => void }) {
  const [tokens, setTokens] = useState<ServiceToken[] | null>(null)
  const [name, setName] = useState('')
  const [scopes, setScopes] = useState<Scope[]>(['events:read'])
  const [lifetime, setLifetime] = useState('2160h')
  const [created, setCreated] = useState<CreatedToken | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [confirmRevoke, setConfirmRevoke] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await people.tokens()
      setTokens(res.tokens)
    } catch (e) {
      setError((e as Error).message)
      setTokens([])
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const toggle = (s: Scope) => setScopes((cur) => (cur.includes(s) ? cur.filter((x) => x !== s) : [...cur, s]))

  const create = async () => {
    setBusy(true)
    setError('')
    try {
      const t = await people.createToken(name.trim(), scopes, lifetime)
      setCreated(t)
      setName('')
      toast(`Token "${t.name}" created`)
      void load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const revoke = async (t: ServiceToken) => {
    try {
      await people.revokeToken(t.id)
      setConfirmRevoke(null)
      if (created?.id === t.id) setCreated(null)
      toast(`Token "${t.name}" revoked`)
      void load()
    } catch (e) {
      toast((e as Error).message)
    }
  }

  const origin = API_URL || window.location.origin
  const grid = '1.1fr 1.5fr 120px 120px 90px'

  return (
    <div style={{ maxWidth: 940 }}>
      <div style={{ marginBottom: 20 }}>
        <h1 style={h1Style}>Access tokens</h1>
        <div style={subStyle}>Credentials for CI and scripts — each can do only what its scopes allow, and only in this organization</div>
      </div>

      <div style={{ ...colHead, marginBottom: 10 }}>New token</div>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          void create()
        }}
        style={{ ...card, padding: '16px 18px', marginBottom: 12, display: 'flex', flexDirection: 'column', gap: 14 }}
      >
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          <TextInput
            required
            placeholder="what uses it, e.g. nightly-report"
            aria-label="Token name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            style={{ flex: '1 1 240px', width: 'auto' }}
          />
          <Select value={lifetime} onChange={(e) => setLifetime(e.target.value)} aria-label="Expires after">
            {LIFETIMES.map((l) => (
              <option key={l.value} value={l.value}>
                expires in {l.label}
              </option>
            ))}
          </Select>
        </div>
        <fieldset style={{ border: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 8 }}>
          <legend style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--muted)', marginBottom: 8 }}>It may</legend>
          {SCOPES.map((s) => (
            <label key={s.value} style={{ display: 'flex', gap: 10, alignItems: 'baseline', fontSize: 13, cursor: 'pointer' }}>
              <input type="checkbox" checked={scopes.includes(s.value)} onChange={() => toggle(s.value)} />
              <span style={{ ...mono, fontSize: 12.5, minWidth: 110 }}>{s.value}</span>
              <span style={{ color: 'var(--muted)' }}>{s.what}</span>
            </label>
          ))}
        </fieldset>
        {error && <Notice>{error}</Notice>}
        <div>
          <Button tone="accent" type="submit" busy={busy} disabled={!name.trim() || scopes.length === 0}>
            Create token
          </Button>
        </div>
      </form>

      {created && (
        <div style={{ background: 'var(--green-bg)', border: '1px solid var(--green)', borderRadius: 10, padding: '12px 16px', marginBottom: 12, display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div style={{ fontSize: 13 }}>
            <strong style={{ color: 'var(--green)' }}>Copy it now.</strong>{' '}
            <span style={{ color: 'var(--muted)' }}>This is the only time the token is shown; the gateway keeps a hash.</span>
          </div>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <span style={{ ...mono, fontSize: 12.5, flex: 1, wordBreak: 'break-all' }}>{created.token}</span>
            <Button
              onClick={async () => {
                try {
                  await navigator.clipboard.writeText(created.token)
                  toast('Token copied')
                } catch {
                  toast('Copy failed — select the token and copy it by hand')
                }
              }}
              style={{ padding: '6px 12px', fontSize: 12.5 }}
            >
              Copy
            </Button>
            <button onClick={() => setCreated(null)} aria-label="Dismiss" style={{ background: 'none', border: 'none', color: 'var(--muted)', fontSize: 16, cursor: 'pointer' }}>
              ×
            </button>
          </div>
          <pre style={{ ...mono, margin: 0, fontSize: 12, background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 7, padding: '10px 12px', overflowX: 'auto' }}>
{`curl -H "Authorization: Bearer $MUTEGATE_TOKEN" \\
  ${origin}/admin/dlp/events`}
          </pre>
        </div>
      )}

      <div style={{ ...colHead, margin: '18px 0 10px' }}>Active tokens</div>
      <div style={{ ...card, overflow: 'hidden' }}>
        <div style={{ display: 'grid', gridTemplateColumns: grid, gap: '0 14px', padding: '9px 18px', borderBottom: '1px solid var(--border)', ...colHead }}>
          <span>Name</span>
          <span>Scopes</span>
          <span>Last used</span>
          <span>Expires</span>
          <span />
        </div>
        {tokens === null ? (
          <div style={{ padding: '22px 18px', color: 'var(--faint)', fontSize: 13 }}>Loading…</div>
        ) : tokens.length === 0 ? (
          <div style={{ padding: '22px 18px', color: 'var(--faint)', fontSize: 13 }}>No tokens. CI that needs the admin API gets one here rather than somebody's password.</div>
        ) : (
          tokens.map((t) => (
            <div key={t.id} className="ap-row-hover" style={{ display: 'grid', gridTemplateColumns: grid, gap: '0 14px', padding: '11px 18px', borderBottom: '1px solid var(--border)', alignItems: 'center' }}>
              <span style={{ minWidth: 0 }}>
                <div style={{ fontSize: 13.5, fontWeight: 500 }}>{t.name}</div>
                <div style={{ ...mono, fontSize: 12, color: 'var(--muted)' }}>{t.hint}</div>
              </span>
              <span style={{ display: 'flex', gap: 5, flexWrap: 'wrap' }}>
                {t.scopes.map((s) => (
                  <ScopeBadge key={s} scope={s} />
                ))}
              </span>
              <span style={{ fontSize: 12, color: 'var(--muted)' }}>{t.last_used_at ? timeAgo(t.last_used_at) : 'never'}</span>
              <span style={{ fontSize: 12, color: 'var(--muted)' }}>{t.expires_at ? fmtTs(t.expires_at).split(',')[0] : 'never'}</span>
              <span style={{ textAlign: 'right' }}>
                {confirmRevoke === t.id ? (
                  <Button tone="danger" onClick={() => revoke(t)} style={{ padding: '5px 11px', fontSize: 12 }}>
                    Revoke
                  </Button>
                ) : (
                  <button onClick={() => setConfirmRevoke(t.id)} className="ap-danger-btn" style={{ background: 'none', border: 'none', color: 'var(--faint)', fontSize: 12.5, cursor: 'pointer', padding: '4px 8px', borderRadius: 5 }}>
                    Revoke
                  </button>
                )}
              </span>
            </div>
          ))
        )}
      </div>
    </div>
  )
}
