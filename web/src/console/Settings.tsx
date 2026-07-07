import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import type { ApertureKey } from '../api'
import { getAdminKey, getApertureKey, setAdminKey, setApertureKey } from '../auth'
import { fmtTs, maskKey } from './format'
import { Badge } from './ui'
import { card, colHead, h1Style, mono, provStyle, subStyle } from './styles'

const inputStyle = {
  background: 'var(--bg)',
  border: '1px solid var(--border)',
  borderRadius: 7,
  padding: '8px 12px',
  fontSize: 13,
  fontFamily: "'IBM Plex Mono', monospace",
  color: 'var(--text)',
} as const

const providers = [
  { id: 'openai', name: 'OpenAI', field: 'openai_api_key', placeholder: 'sk-proj-…' },
  { id: 'anthropic', name: 'Anthropic', field: 'anthropic_api_key', placeholder: 'sk-ant-…' },
  { id: 'groq', name: 'Groq', field: 'groq_api_key', placeholder: 'gsk_…' },
] as const

export function Settings({ noDB, toast }: { noDB: boolean; toast: (msg: string) => void }) {
  const [configured, setConfigured] = useState<string[]>([])
  const [vals, setVals] = useState<Record<string, string>>({})
  const [keys, setKeys] = useState<ApertureKey[]>([])
  const [newKeyName, setNewKeyName] = useState('')
  const [createdKey, setCreatedKey] = useState<ApertureKey | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null)
  const [gwAperture, setGwAperture] = useState(getApertureKey)
  const [gwAdmin, setGwAdmin] = useState(getAdminKey)
  const [unauthorized, setUnauthorized] = useState(false)

  const load = useCallback(async () => {
    try {
      const cfg = await api.config()
      setConfigured(cfg.configured_providers ?? [])
      setUnauthorized(false)
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) setUnauthorized(true)
    }
    try {
      const res = await api.listKeys()
      setKeys(res.keys)
    } catch {
      setKeys([])
    }
  }, [])

  useEffect(() => {
    // load() only sets state after awaited API calls resolve.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load()
  }, [load])

  const saveProvider = async (field: string, id: string) => {
    const v = vals[id]?.trim()
    if (!v) return
    try {
      await api.setConfig({ [field]: v })
      setVals((s) => ({ ...s, [id]: '' }))
      toast(`${id} key saved`)
      load()
    } catch (e) {
      toast(`Save failed: ${(e as Error).message}`)
    }
  }

  const clearAll = async () => {
    try {
      await api.clearConfig()
      toast('All provider keys cleared')
      load()
    } catch (e) {
      toast(`Clear failed: ${(e as Error).message}`)
    }
  }

  const createKey = async () => {
    if (!newKeyName.trim()) return
    try {
      const k = await api.createKey(newKeyName.trim())
      setCreatedKey(k)
      setNewKeyName('')
      toast(`Key "${k.name}" created`)
      load()
    } catch (e) {
      toast(`Create failed: ${(e as Error).message}`)
    }
  }

  const deleteKey = async (id: string) => {
    try {
      await api.deleteKey(id)
      setConfirmDelete(null)
      toast('Key deleted')
      load()
    } catch (e) {
      toast(`Delete failed: ${(e as Error).message}`)
    }
  }

  return (
    <div style={{ maxWidth: 820 }}>
      <div style={{ marginBottom: 20 }}>
        <h1 style={h1Style}>Settings &amp; Keys</h1>
        <div style={subStyle}>Provider credentials and gateway access keys</div>
      </div>

      {noDB && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, background: 'var(--amber-bg)', border: '1px solid var(--amber)', borderRadius: 10, padding: '12px 16px', marginBottom: 22, fontSize: 13 }}>
          <span style={{ width: 8, height: 8, borderRadius: '50%', background: 'var(--amber)', display: 'inline-block', flexShrink: 0 }} />
          <span>
            <strong style={{ color: 'var(--amber)' }}>Running without a database.</strong> Keys and policies live in memory and will be lost on restart. Set{' '}
            <span style={{ ...mono, fontSize: 12 }}>DATABASE_URL</span> to persist.
          </span>
        </div>
      )}

      {/* Gateway access — keys this console uses */}
      <div style={{ ...colHead, marginBottom: 10 }}>Console access</div>
      <div style={{ ...card, padding: '16px 18px', marginBottom: 30, display: 'flex', flexDirection: 'column', gap: 10 }}>
        {unauthorized && (
          <div style={{ fontSize: 13, color: 'var(--red)' }}>
            Unauthorized — paste the Admin API key from the server startup log.
          </div>
        )}
        <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
          <span style={{ fontSize: 12.5, color: 'var(--muted)', width: 130, flexShrink: 0 }}>Admin API key</span>
          <input
            type="password"
            value={gwAdmin}
            onChange={(e) => {
              setGwAdmin(e.target.value)
              setAdminKey(e.target.value)
            }}
            onBlur={load}
            placeholder="admin-… (from server log)"
            aria-label="Admin API key"
            style={{ ...inputStyle, flex: 1 }}
          />
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
          <span style={{ fontSize: 12.5, color: 'var(--muted)', width: 130, flexShrink: 0 }}>Aperture API key</span>
          <input
            type="password"
            value={gwAperture}
            onChange={(e) => {
              setGwAperture(e.target.value)
              setApertureKey(e.target.value)
            }}
            placeholder="ap-… (used by the playground chat)"
            aria-label="Aperture API key"
            style={{ ...inputStyle, flex: 1 }}
          />
        </div>
      </div>

      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 10 }}>
        <div style={colHead}>Provider keys</div>
        <button
          onClick={clearAll}
          className="ap-danger-btn"
          style={{ background: 'none', border: 'none', color: 'var(--faint)', fontSize: 12.5, cursor: 'pointer', padding: '4px 8px', borderRadius: 5 }}
        >
          Clear all
        </button>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginBottom: 30 }}>
        {providers.map((p) => {
          const isSet = configured.includes(p.id)
          const s = provStyle(p.id)
          return (
            <div key={p.id} style={{ display: 'flex', alignItems: 'center', gap: 14, ...card, borderRadius: 10, padding: '13px 16px' }}>
              <span style={{ width: 82, textAlign: 'center', flexShrink: 0 }}>
                <Badge bg={s.bg} fg={s.fg}>{p.name}</Badge>
              </span>
              <input
                type="password"
                value={vals[p.id] ?? ''}
                onChange={(e) => setVals((st) => ({ ...st, [p.id]: e.target.value }))}
                placeholder={isSet ? '•••••••• (configured — paste to replace)' : p.placeholder}
                aria-label={`${p.name} API key`}
                style={{ ...inputStyle, flex: 1 }}
              />
              <span style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: isSet ? 'var(--green)' : 'var(--faint)', flexShrink: 0, width: 96 }}>
                <span style={{ width: 7, height: 7, borderRadius: '50%', background: isSet ? 'var(--green)' : 'var(--faint)', display: 'inline-block' }} />
                {isSet ? 'configured' : 'not set'}
              </span>
              <button
                onClick={() => saveProvider(p.field, p.id)}
                disabled={!vals[p.id]?.trim()}
                className="ap-save-btn"
                style={{ background: 'var(--bg3)', border: '1px solid var(--border2)', color: 'var(--text)', padding: '7px 14px', borderRadius: 7, fontSize: 12.5, fontWeight: 600, cursor: 'pointer' }}
              >
                Save
              </button>
            </div>
          )
        })}
      </div>

      <div style={{ ...colHead, marginBottom: 10 }}>Aperture keys</div>
      {createdKey && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, background: 'var(--green-bg)', border: '1px solid var(--green)', borderRadius: 10, padding: '12px 16px', marginBottom: 12, fontSize: 13 }}>
          <span style={{ flexShrink: 0, color: 'var(--green)', fontWeight: 600 }}>Copy it now:</span>
          <span style={{ ...mono, fontSize: 12.5, flex: 1, wordBreak: 'break-all' }}>{createdKey.aperture_key}</span>
          <button
            onClick={() => {
              navigator.clipboard?.writeText(createdKey.aperture_key)
              toast('Copied to clipboard')
            }}
            style={{ background: 'var(--bg3)', border: '1px solid var(--border2)', color: 'var(--text)', padding: '5px 12px', borderRadius: 6, fontSize: 12, cursor: 'pointer' }}
          >
            Copy
          </button>
          <button onClick={() => setCreatedKey(null)} aria-label="Dismiss" style={{ background: 'none', border: 'none', color: 'var(--muted)', fontSize: 16, cursor: 'pointer' }}>×</button>
        </div>
      )}
      <div style={{ ...card, overflow: 'hidden', marginBottom: 14 }}>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1.6fr 160px 110px', gap: '0 14px', padding: '9px 18px', borderBottom: '1px solid var(--border)', ...colHead }}>
          <span>Name</span><span>Key</span><span>Created</span><span></span>
        </div>
        {keys.length === 0 ? (
          <div style={{ padding: '24px 18px', color: 'var(--faint)', fontSize: 13 }}>
            {noDB
              ? 'Key management requires PostgreSQL. In no-DB mode use the single APERTURE_API_KEY from the server log.'
              : 'No keys yet — create one below.'}
          </div>
        ) : (
          keys.map((k) => (
            <div key={k.id} className="ap-row-hover" style={{ display: 'grid', gridTemplateColumns: '1fr 1.6fr 160px 110px', gap: '0 14px', padding: '11px 18px', borderBottom: '1px solid var(--border)', alignItems: 'center' }}>
              <span style={{ ...mono, fontSize: 13, fontWeight: 500 }}>{k.name || '—'}</span>
              <span style={{ ...mono, fontSize: 12.5, color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{maskKey(k.aperture_key)}</span>
              <span style={{ ...mono, fontSize: 12, color: 'var(--muted)', fontVariantNumeric: 'tabular-nums' }}>{fmtTs(k.created_at).split(',')[0]}</span>
              <span style={{ textAlign: 'right' }}>
                {confirmDelete === k.id ? (
                  <span style={{ display: 'inline-flex', gap: 6, alignItems: 'center' }}>
                    <button onClick={() => deleteKey(k.id)} style={{ background: 'var(--red)', color: '#fff', border: 'none', padding: '5px 11px', borderRadius: 6, fontSize: 12, fontWeight: 600, cursor: 'pointer' }}>Delete</button>
                    <button onClick={() => setConfirmDelete(null)} style={{ background: 'none', border: '1px solid var(--border2)', color: 'var(--muted)', padding: '5px 10px', borderRadius: 6, fontSize: 12, cursor: 'pointer' }}>Cancel</button>
                  </span>
                ) : (
                  <button
                    onClick={() => setConfirmDelete(k.id)}
                    className="ap-danger-btn"
                    style={{ background: 'none', border: 'none', color: 'var(--faint)', fontSize: 12.5, cursor: 'pointer', padding: '4px 8px', borderRadius: 5 }}
                  >
                    Delete
                  </button>
                )}
              </span>
            </div>
          ))
        )}
      </div>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        <input
          value={newKeyName}
          onChange={(e) => setNewKeyName(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && createKey()}
          placeholder="key name (e.g. qa-bot)"
          aria-label="New key name"
          style={{ ...inputStyle, background: 'var(--bg2)', flex: '0 0 220px' }}
        />
        <button
          onClick={createKey}
          disabled={!newKeyName.trim()}
          className="ap-accent-btn"
          style={{ background: 'var(--accent)', color: '#0b0e13', border: 'none', padding: '8px 18px', borderRadius: 7, fontSize: 13, fontWeight: 600, cursor: 'pointer' }}
        >
          Create key
        </button>
      </div>
    </div>
  )
}
