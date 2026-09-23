// The keys agents use to reach the gateway. A table to see them, and a panel
// per key for what belongs to that key alone: its ceiling, its policy, and
// deleting it — so nothing on this page grows a row per key.
import { useCallback, useEffect, useState } from 'react'
import { api } from '../api'
import type { ApertureKey, LimitsResponse } from '../api'
import { Link } from '../router'
import { fmtCost, fmtTs, maskKey } from './format'
import { card, h1Style, mono, subStyle } from './styles'
import { Button, Notice, TextInput } from './forms'
import { LimitRow } from './LimitsCard'

function limitText(l?: { budget_daily_usd?: number; requests_per_minute?: number }): string | null {
  if (!l) return null
  const parts = []
  if (l.budget_daily_usd) parts.push(`${fmtCost(l.budget_daily_usd)} a day`)
  if (l.requests_per_minute) parts.push(`${l.requests_per_minute} req/min`)
  return parts.length ? parts.join(' · ') : null
}

export function ApiKeys({ noDB, toast }: { noDB: boolean; toast: (msg: string) => void }) {
  const [keys, setKeys] = useState<ApertureKey[] | null>(null)
  const [limits, setLimits] = useState<LimitsResponse | null>(null)
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [created, setCreated] = useState<ApertureKey | null>(null)
  const [openID, setOpenID] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setKeys((await api.listKeys()).keys)
    } catch {
      setKeys([])
    }
    try {
      setLimits(await api.limits())
    } catch {
      setLimits(null)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const create = async () => {
    setBusy(true)
    setError('')
    try {
      const k = await api.createKey(name.trim())
      setCreated(k)
      setName('')
      setCreating(false)
      toast(`Key "${k.name}" created`)
      void load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const open = keys?.find((k) => k.id === openID) ?? null

  return (
    <div style={{ display: 'flex', gap: 20, alignItems: 'flex-start' }}>
      <div style={{ flex: 1, minWidth: 0, maxWidth: 820 }}>
        <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 16, marginBottom: 20 }}>
          <div>
            <h1 style={h1Style}>API keys</h1>
            <div style={subStyle}>Keys your agents use to reach the gateway. Each is shown once, when it's created.</div>
          </div>
          {!noDB && !creating && (
            <Button tone="accent" onClick={() => setCreating(true)}>
              Create key
            </Button>
          )}
        </div>

        {noDB && (
          <div style={{ marginBottom: 16 }}>
            <Notice tone="warn">
              Creating keys needs PostgreSQL. Without it the gateway has one key, APERTURE_API_KEY, printed in its log at
              startup.
            </Notice>
          </div>
        )}

        {creating && (
          <form
            onSubmit={(e) => {
              e.preventDefault()
              void create()
            }}
            style={{ ...card, padding: '14px 16px', marginBottom: 16, display: 'flex', flexDirection: 'column', gap: 10 }}
          >
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
              <TextInput
                required
                autoFocus
                placeholder="What uses it, e.g. support-agent"
                aria-label="Key name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                style={{ flex: '1 1 260px', width: 'auto' }}
              />
              <Button tone="accent" type="submit" busy={busy} disabled={!name.trim()}>
                Create
              </Button>
              <Button type="button" onClick={() => setCreating(false)}>
                Cancel
              </Button>
            </div>
            {error && <Notice>{error}</Notice>}
          </form>
        )}

        {created && (
          <div style={{ background: 'var(--green-bg)', border: '1px solid var(--green)', borderRadius: 10, padding: '12px 16px', marginBottom: 16, display: 'flex', alignItems: 'center', gap: 10 }}>
            <span style={{ fontSize: 13, color: 'var(--green)', fontWeight: 600, flexShrink: 0 }}>Copy it now</span>
            <span style={{ ...mono, fontSize: 12.5, flex: 1, wordBreak: 'break-all' }}>{created.aperture_key}</span>
            <Button
              onClick={async () => {
                try {
                  await navigator.clipboard.writeText(created.aperture_key)
                  toast('Key copied')
                } catch {
                  toast('Copy failed — select the key and copy it by hand')
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
        )}

        <div style={{ ...card, overflow: 'hidden' }}>
          <div style={{ display: 'grid', gridTemplateColumns: '1.3fr 1.4fr 1.2fr 0.8fr', gap: '0 14px', padding: '10px 18px', borderBottom: '1px solid var(--border)', fontSize: 12.5, color: 'var(--faint)' }}>
            <span>Name</span>
            <span>Key</span>
            <span>Limit</span>
            <span>Created</span>
          </div>
          {keys === null ? (
            <div style={{ padding: '20px 18px', color: 'var(--faint)', fontSize: 13 }}>Loading…</div>
          ) : keys.length === 0 ? (
            <div style={{ padding: '22px 18px', color: 'var(--faint)', fontSize: 13 }}>
              {noDB ? 'No keys to list without a database.' : 'No keys yet. Create one for each agent, so its traffic is told apart from the others.'}
            </div>
          ) : (
            keys.map((k) => {
              const own = limitText(limits?.keys[k.id])
              const selected = k.id === openID
              return (
                <button
                  key={k.id}
                  onClick={() => setOpenID(selected ? null : k.id)}
                  className="ap-row-hover"
                  aria-expanded={selected}
                  style={{ display: 'grid', gridTemplateColumns: '1.3fr 1.4fr 1.2fr 0.8fr', gap: '0 14px', width: '100%', padding: '12px 18px', border: 'none', borderBottom: '1px solid var(--border)', background: selected ? 'var(--bg3)' : 'none', textAlign: 'left', cursor: 'pointer', color: 'var(--text)', fontSize: 13.5, alignItems: 'center' }}
                >
                  <span style={{ fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{k.name || '—'}</span>
                  <span style={{ ...mono, fontSize: 12.5, color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{maskKey(k.aperture_key)}</span>
                  <span style={{ fontSize: 13, color: own ? 'var(--text)' : 'var(--muted)' }}>{own ?? 'Default'}</span>
                  <span style={{ fontSize: 13, color: 'var(--muted)', fontVariantNumeric: 'tabular-nums' }}>{fmtTs(k.created_at).split(',')[0]}</span>
                </button>
              )
            })
          )}
        </div>
      </div>

      {open && (
        <KeyPanel
          key={open.id}
          apiKey={open}
          limits={limits}
          toast={toast}
          onClose={() => setOpenID(null)}
          onChanged={load}
          onDeleted={() => {
            setOpenID(null)
            void load()
          }}
        />
      )}
    </div>
  )
}

/** Everything that belongs to one key: its ceiling, its policy, deleting it. */
function KeyPanel({
  apiKey,
  limits,
  toast,
  onClose,
  onChanged,
  onDeleted,
}: {
  apiKey: ApertureKey
  limits: LimitsResponse | null
  toast: (msg: string) => void
  onClose: () => void
  onChanged: () => void
  onDeleted: () => void
}) {
  const [saving, setSaving] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const own = limits?.keys[apiKey.id]
  const spent = limits?.spent_usd[apiKey.id] ?? 0
  const fallback = limitText(limits?.default) ?? 'no limit'

  const section = { fontSize: 12.5, fontWeight: 600, color: 'var(--muted)', margin: '18px 0 8px' } as const

  return (
    <aside
      aria-label={`Key ${apiKey.name}`}
      style={{ width: 360, flexShrink: 0, ...card, padding: 20, position: 'sticky', top: 28, animation: 'ap-drawer 0.18s ease-out' }}
    >
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 10 }}>
        <div style={{ minWidth: 0 }}>
          <div className="ap-display" style={{ fontSize: 20, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{apiKey.name}</div>
          <div style={{ ...mono, fontSize: 12, color: 'var(--muted)', marginTop: 2 }}>{maskKey(apiKey.aperture_key)}</div>
        </div>
        <button onClick={onClose} aria-label="Close" style={{ background: 'none', border: 'none', color: 'var(--muted)', fontSize: 18, cursor: 'pointer', lineHeight: 1 }}>
          ×
        </button>
      </div>
      <div style={{ fontSize: 12.5, color: 'var(--muted)', marginTop: 8 }}>Created {fmtTs(apiKey.created_at)}</div>

      <div style={section}>Limit</div>
      <div style={{ fontSize: 12.5, color: 'var(--muted)', marginBottom: 8 }}>
        {own ? 'Its own ceiling.' : `Uses the default: ${fallback}.`} Spent today: {fmtCost(spent)}.
      </div>
      <div style={{ border: '1px solid var(--border)', borderRadius: 10, overflow: 'hidden' }}>
        <LimitRow
          key={`${own?.budget_daily_usd ?? ''}:${own?.requests_per_minute ?? ''}`}
          label={apiKey.name}
          hideLabel
          value={own ?? {}}
          saving={saving}
          onSave={async (l) => {
            setSaving(true)
            try {
              await api.putKeyLimits(apiKey.id, l)
              toast('Limit saved — applied to live traffic')
              onChanged()
            } catch (e) {
              toast(`Save failed: ${(e as Error).message}`)
            } finally {
              setSaving(false)
            }
          }}
          onClear={
            own
              ? async () => {
                  try {
                    await api.deleteKeyLimits(apiKey.id)
                    toast('Key uses the default limit again')
                    onChanged()
                  } catch (e) {
                    toast(`Reset failed: ${(e as Error).message}`)
                  }
                }
              : undefined
          }
        />
      </div>

      <div style={section}>Policy</div>
      <Link to={`/app/policies?key=${encodeURIComponent(apiKey.id)}`} style={{ fontSize: 13.5 }}>
        Open this key's policy →
      </Link>

      <div style={section}>Delete</div>
      <div style={{ fontSize: 12.5, color: 'var(--muted)', marginBottom: 10 }}>
        Agents using it are refused from the next request. Its incidents and history stay.
      </div>
      {confirmDelete ? (
        <div style={{ display: 'flex', gap: 8 }}>
          <Button
            tone="danger"
            onClick={async () => {
              try {
                await api.deleteKey(apiKey.id)
                toast(`Key "${apiKey.name}" deleted`)
                onDeleted()
              } catch (e) {
                toast(`Delete failed: ${(e as Error).message}`)
              }
            }}
          >
            Delete key
          </Button>
          <Button onClick={() => setConfirmDelete(false)}>Cancel</Button>
        </div>
      ) : (
        <Button onClick={() => setConfirmDelete(true)}>Delete key…</Button>
      )}
    </aside>
  )
}
