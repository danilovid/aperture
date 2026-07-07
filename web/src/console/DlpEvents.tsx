import { useCallback, useEffect, useMemo, useState } from 'react'
import { api } from '../api'
import type { DLPEvent } from '../api'
import { fmtTs, timeAgo } from './format'
import { ActionBadge, EmptyState } from './ui'
import { actionStyle, card, colHead, h1Style, mono, subStyle } from './styles'

const grid = '92px 110px 1fr 130px 96px 1.2fr'

const selectStyle = {
  background: 'var(--bg2)',
  border: '1px solid var(--border)',
  borderRadius: 7,
  padding: '7px 10px',
  fontSize: 13,
  color: 'var(--text)',
  cursor: 'pointer',
} as const

export function DlpEvents() {
  const [events, setEvents] = useState<DLPEvent[]>([])
  const [fAction, setFAction] = useState('all')
  const [fRule, setFRule] = useState('all')
  const [fKey, setFKey] = useState('all')
  const [sel, setSel] = useState<DLPEvent | null>(null)
  const [loaded, setLoaded] = useState(false)

  const fetchEvents = useCallback(async () => {
    try {
      const { events } = await api.dlpEvents({ action: fAction, rule: fRule, key_id: fKey })
      setEvents(events)
    } finally {
      setLoaded(true)
    }
  }, [fAction, fRule, fKey])

  useEffect(() => {
    void fetchEvents().catch(() => {})
  }, [fetchEvents])

  // Options are collected from the visible data so filters stay relevant.
  const ruleOptions = useMemo(() => [...new Set(events.map((e) => e.rule))].sort(), [events])
  const keyOptions = useMemo(() => [...new Set(events.map((e) => e.key_id))].sort(), [events])

  return (
    <div style={{ display: 'flex', gap: 20, alignItems: 'flex-start' }}>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ marginBottom: 20 }}>
          <h1 style={h1Style}>DLP Events</h1>
          <div style={subStyle}>Sensitive data caught before leaving your network</div>
        </div>

        <div style={{ display: 'flex', gap: 10, marginBottom: 16, flexWrap: 'wrap' }}>
          <select value={fAction} onChange={(e) => setFAction(e.target.value)} aria-label="Filter by action" style={selectStyle}>
            <option value="all">All actions</option>
            <option value="blocked">Blocked</option>
            <option value="redacted">Redacted</option>
            <option value="alerted">Alert only</option>
          </select>
          <select value={fRule} onChange={(e) => setFRule(e.target.value)} aria-label="Filter by rule" style={selectStyle}>
            <option value="all">All rules</option>
            {ruleOptions.map((r) => (
              <option key={r} value={r}>{r}</option>
            ))}
          </select>
          <select value={fKey} onChange={(e) => setFKey(e.target.value)} aria-label="Filter by key" style={selectStyle}>
            <option value="all">All keys</option>
            {keyOptions.map((k) => (
              <option key={k} value={k}>{k}</option>
            ))}
          </select>
          <div style={{ flex: 1 }} />
          <div style={{ alignSelf: 'center', ...mono, fontSize: 12, color: 'var(--faint)' }}>
            {events.length} events
          </div>
        </div>

        {loaded && events.length === 0 ? (
          <EmptyState
            title="No incidents — your traffic is clean"
            sub="Nothing matched the active filters and policies. That's the goal."
          />
        ) : (
          <div style={{ ...card, overflow: 'hidden' }}>
            <div style={{ display: 'grid', gridTemplateColumns: grid, gap: '0 14px', padding: '9px 18px', borderBottom: '1px solid var(--border)', ...colHead }}>
              <span>Time</span><span>Key</span><span>Model</span><span>Rule</span><span>Action</span><span>Sample</span>
            </div>
            {events.map((e) => (
              <div
                key={e.id}
                onClick={() => setSel(sel?.id === e.id ? null : e)}
                role="button"
                tabIndex={0}
                onKeyDown={(ev) => ev.key === 'Enter' && setSel(sel?.id === e.id ? null : e)}
                className="ap-row-hover"
                style={{
                  display: 'grid',
                  gridTemplateColumns: grid,
                  gap: '0 14px',
                  padding: '11px 18px',
                  borderBottom: '1px solid var(--border)',
                  alignItems: 'center',
                  cursor: 'pointer',
                  background: sel?.id === e.id ? 'var(--bg3)' : 'transparent',
                }}
              >
                <span style={{ ...mono, fontSize: 12, color: 'var(--muted)', fontVariantNumeric: 'tabular-nums' }}>{timeAgo(e.ts)}</span>
                <span style={{ ...mono, fontSize: 12.5, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{e.key_id}</span>
                <span style={{ ...mono, fontSize: 12.5, color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{e.model}</span>
                <span style={{ ...mono, fontSize: 12, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{e.rule}</span>
                <span><ActionBadge action={e.action} /></span>
                <span style={{ ...mono, fontSize: 12, color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{e.masked_sample}</span>
              </div>
            ))}
          </div>
        )}
      </div>

      {sel && (
        <div style={{ width: 340, flexShrink: 0, ...card, padding: 20, position: 'sticky', top: 28, animation: 'ap-drawer 0.18s ease-out' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
            <ActionBadge action={sel.action} />
            <button
              onClick={() => setSel(null)}
              aria-label="Close details"
              className="ap-ghost-btn"
              style={{ background: 'none', border: 'none', color: 'var(--muted)', fontSize: 18, cursor: 'pointer', padding: '2px 6px', borderRadius: 5 }}
            >
              ×
            </button>
          </div>
          <div style={{ fontWeight: 600, fontSize: 15, marginBottom: 4, ...mono }}>{sel.rule}</div>
          <div style={{ color: 'var(--muted)', fontSize: 13, marginBottom: 18 }}>
            {sel.group === 'secrets' ? 'Credential detected in outbound content' : sel.group === 'pii' ? 'Personal data detected in outbound content' : 'Custom rule match'}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '8px 16px', fontSize: 13, marginBottom: 18 }}>
            <span style={{ color: 'var(--faint)' }}>Time</span>
            <span style={{ ...mono, fontSize: 12.5 }}>{fmtTs(sel.ts)}</span>
            <span style={{ color: 'var(--faint)' }}>Key</span>
            <span style={{ ...mono, fontSize: 12.5 }}>{sel.key_id}</span>
            <span style={{ color: 'var(--faint)' }}>Model</span>
            <span style={{ ...mono, fontSize: 12.5 }}>{sel.model}</span>
            <span style={{ color: 'var(--faint)' }}>Provider</span>
            <span style={{ ...mono, fontSize: 12.5 }}>{sel.provider}</span>
          </div>
          <div style={{ ...colHead, marginBottom: 8 }}>Masked sample</div>
          <div style={{ ...mono, fontSize: 12.5, background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 8, padding: '12px 14px', wordBreak: 'break-all', color: actionStyle(sel.action).fg, marginBottom: 14 }}>
            {sel.masked_sample}
          </div>
          <div style={{ fontSize: 12.5, color: 'var(--faint)', display: 'flex', alignItems: 'center', gap: 7 }}>
            <span style={{ width: 6, height: 6, borderRadius: '50%', background: 'var(--green)', display: 'inline-block', flexShrink: 0 }} />
            Original content never stored — only the mask.
          </div>
        </div>
      )}
    </div>
  )
}
