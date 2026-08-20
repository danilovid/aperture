import { useCallback, useEffect, useMemo, useState } from 'react'
import { api } from '../api'
import type { DLPEvent } from '../api'
import { fmtTs, timeAgo } from './format'
import { ActionBadge, EmptyState } from './ui'
import { actionStyle, card, colHead, h1Style, mono, subStyle } from './styles'

const grid = '84px 104px 104px 1fr 122px 92px 1fr'

const selectStyle = {
  background: 'var(--bg2)',
  border: '1px solid var(--border)',
  borderRadius: 7,
  padding: '7px 10px',
  fontSize: 13,
  color: 'var(--text)',
  cursor: 'pointer',
} as const

export function DlpEvents({ toast }: { toast: (msg: string) => void }) {
  const [events, setEvents] = useState<DLPEvent[]>([])
  const [fAction, setFAction] = useState('all')
  const [fRule, setFRule] = useState('all')
  const [fKey, setFKey] = useState('all')
  const [fAgent, setFAgent] = useState('all')
  const [fDirection, setFDirection] = useState('all')
  const [sel, setSel] = useState<DLPEvent | null>(null)
  const [loaded, setLoaded] = useState(false)
  const [muting, setMuting] = useState(false)

  const fetchEvents = useCallback(async () => {
    try {
      const { events } = await api.dlpEvents({
        action: fAction, rule: fRule, key_id: fKey, agent: fAgent, direction: fDirection,
      })
      setEvents(events)
    } finally {
      setLoaded(true)
    }
  }, [fAction, fRule, fKey, fAgent, fDirection])

  useEffect(() => {
    void fetchEvents().catch(() => {})
  }, [fetchEvents])

  const mute = async (e: DLPEvent) => {
    setMuting(true)
    try {
      await api.muteRule(e.key_id, e.rule)
      toast(`Muted ${e.rule} for ${e.key_id} — applied to live traffic`)
      await fetchEvents()
    } catch (err) {
      toast(`Mute failed: ${(err as Error).message}`)
    } finally {
      setMuting(false)
    }
  }

  const unmute = async (e: DLPEvent) => {
    setMuting(true)
    try {
      await api.unmuteRule(e.key_id, e.rule)
      toast(`Un-muted ${e.rule} for ${e.key_id}`)
      await fetchEvents()
    } catch (err) {
      toast(`Un-mute failed: ${(err as Error).message}`)
    } finally {
      setMuting(false)
    }
  }

  // Options are collected from the visible data so filters stay relevant.
  const ruleOptions = useMemo(() => [...new Set(events.map((e) => e.rule))].sort(), [events])
  const keyOptions = useMemo(() => [...new Set(events.map((e) => e.key_id))].sort(), [events])
  const agentOptions = useMemo(
    () => [...new Set(events.map((e) => e.agent).filter(Boolean) as string[])].sort(),
    [events],
  )

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
            <option value="suppressed">Muted / allowlisted</option>
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
          {agentOptions.length > 0 && (
            <select value={fAgent} onChange={(e) => setFAgent(e.target.value)} aria-label="Filter by agent" style={selectStyle}>
              <option value="all">All agents</option>
              {agentOptions.map((a) => (
                <option key={a} value={a}>{a}</option>
              ))}
            </select>
          )}
          <select
            value={fDirection}
            onChange={(e) => setFDirection(e.target.value)}
            aria-label="Filter by direction"
            style={selectStyle}
          >
            <option value="all">Both directions</option>
            <option value="request">Sent by the agent</option>
            <option value="response">Sent back by the model</option>
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
              <span>Time</span><span>Key</span><span>Agent</span><span>Model</span><span>Rule</span><span>Action</span><span>Sample</span>
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
                <span style={{ ...mono, fontSize: 12.5, color: e.agent ? 'var(--accent)' : 'var(--faint)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{e.agent || '—'}</span>
                <span style={{ ...mono, fontSize: 12.5, color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{e.model}</span>
                <span style={{ ...mono, fontSize: 12, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  <span
                    title={e.direction === 'response' ? 'in the model’s answer' : 'in what the agent sent'}
                    style={{ color: e.direction === 'response' ? 'var(--amber)' : 'var(--faint)', marginRight: 6 }}
                  >
                    {e.direction === 'response' ? '←' : '→'}
                  </span>
                  {e.rule}
                </span>
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
            {sel.group === 'secrets'
              ? 'Credential detected'
              : sel.group === 'pii'
                ? 'Personal data detected'
                : 'Custom rule match'}{' '}
            {sel.direction === 'response' ? 'in the model’s answer' : 'in what the agent sent'}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '8px 16px', fontSize: 13, marginBottom: 18 }}>
            <span style={{ color: 'var(--faint)' }}>Time</span>
            <span style={{ ...mono, fontSize: 12.5 }}>{fmtTs(sel.ts)}</span>
            <span style={{ color: 'var(--faint)' }}>Key</span>
            <span style={{ ...mono, fontSize: 12.5 }}>{sel.key_id}</span>
            {sel.agent && (
              <>
                <span style={{ color: 'var(--faint)' }}>Agent</span>
                <span style={{ ...mono, fontSize: 12.5, color: 'var(--accent)' }}>{sel.agent}</span>
              </>
            )}
            {sel.session && (
              <>
                <span style={{ color: 'var(--faint)' }}>Session</span>
                <span style={{ ...mono, fontSize: 12.5 }}>{sel.session}</span>
              </>
            )}
            <span style={{ color: 'var(--faint)' }}>Direction</span>
            <span style={{ ...mono, fontSize: 12.5, color: sel.direction === 'response' ? 'var(--amber)' : undefined }}>
              {sel.direction === 'response' ? 'response' : 'request'}
            </span>
            <span style={{ color: 'var(--faint)' }}>Model</span>
            <span style={{ ...mono, fontSize: 12.5 }}>{sel.model}</span>
            <span style={{ color: 'var(--faint)' }}>Provider</span>
            <span style={{ ...mono, fontSize: 12.5 }}>{sel.provider}</span>
          </div>
          <div style={{ ...colHead, marginBottom: 8 }}>Masked sample</div>
          <div style={{ ...mono, fontSize: 12.5, background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 8, padding: '12px 14px', wordBreak: 'break-all', color: actionStyle(sel.action).fg, marginBottom: 14 }}>
            {sel.masked_sample}
          </div>
          <div style={{ fontSize: 12.5, color: 'var(--faint)', display: 'flex', alignItems: 'center', gap: 7, marginBottom: 16 }}>
            <span style={{ width: 6, height: 6, borderRadius: '50%', background: 'var(--green)', display: 'inline-block', flexShrink: 0 }} />
            Original content never stored — only the mask.
          </div>

          {sel.action === 'suppressed' ? (
            <button
              onClick={() => unmute(sel)}
              disabled={muting}
              className="ap-save-btn"
              style={{ width: '100%', background: 'var(--bg3)', border: '1px solid var(--border2)', color: 'var(--text)', padding: '9px 14px', borderRadius: 8, fontSize: 13, fontWeight: 600, cursor: 'pointer' }}
            >
              {muting ? 'Working…' : `Un-mute ${sel.rule} for ${sel.key_id}`}
            </button>
          ) : (
            <>
              <button
                onClick={() => mute(sel)}
                disabled={muting}
                className="ap-save-btn"
                style={{ width: '100%', background: 'var(--bg3)', border: '1px solid var(--border2)', color: 'var(--text)', padding: '9px 14px', borderRadius: 8, fontSize: 13, fontWeight: 600, cursor: 'pointer' }}
              >
                {muting ? 'Muting…' : `Mute ${sel.rule} for this key`}
              </button>
              <div style={{ fontSize: 12, color: 'var(--faint)', marginTop: 8, lineHeight: 1.5 }}>
                False positive? Muting stops this detector for{' '}
                <span style={{ ...mono, fontSize: 11.5 }}>{sel.key_id}</span> right away. Matches keep
                being recorded as <b>MUTED</b>, so nothing becomes invisible.
              </div>
            </>
          )}
        </div>
      )}
    </div>
  )
}
