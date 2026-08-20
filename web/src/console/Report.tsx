import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import type { AuditReport, Period, PolicyActions } from '../api'
import { fmtNum, timeAgo } from './format'
import { EmptyState, Segmented, Skeleton } from './ui'
import { card, colHead, h1Style, mono, subStyle } from './styles'

const groups = [
  { id: 'secrets', label: 'Secrets', field: 'secrets' as const },
  { id: 'pii', label: 'PII', field: 'pii' as const },
  { id: 'custom', label: 'Custom', field: 'custom' as const },
]

const periods: { value: Period; label: string }[] = [
  { value: '24h', label: '24h' },
  { value: '7d', label: '7d' },
  { value: '30d', label: '30d' },
]

/** One group's headline: what flipping it to block would have cost. */
function ImpactCard({
  label,
  current,
  events,
  wouldBlock,
  keys,
}: {
  label: string
  current: string
  events: number
  wouldBlock: number
  keys: number
}) {
  const alreadyBlocking = current === 'block'
  const off = current === 'off'
  const tone = wouldBlock > 0 ? 'var(--red)' : 'var(--muted)'
  return (
    <div style={{ ...card, padding: '16px 18px' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 6 }}>
        <span style={{ fontSize: 12.5, color: 'var(--muted)', fontWeight: 500 }}>{label}</span>
        <span style={{ ...mono, fontSize: 11, color: 'var(--faint)' }}>now: {current || 'off'}</span>
      </div>
      <div style={{ ...mono, fontSize: 22, fontWeight: 600, color: tone, fontVariantNumeric: 'tabular-nums' }}>
        {alreadyBlocking ? '—' : fmtNum(wouldBlock)}
      </div>
      <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 4, minHeight: 32 }}>
        {alreadyBlocking
          ? 'already blocking — no change'
          : off
            ? `detector is off; ${fmtNum(events)} matches would be found`
            : wouldBlock > 0
              ? `requests would be rejected, across ${keys} key${keys === 1 ? '' : 's'}`
              : 'nothing would change'}
      </div>
    </div>
  )
}

function PolicyCell({ p }: { p: PolicyActions }) {
  return (
    <span style={{ ...mono, fontSize: 11.5, color: 'var(--muted)' }}>
      {p.secrets || 'off'} · {p.pii || 'off'} · {p.custom || 'off'}
    </span>
  )
}

/** Plain-text digest, for pasting into a ticket or a team channel. */
function toMarkdown(r: AuditReport): string {
  const lines: string[] = []
  lines.push(`# Aperture audit report — ${r.period}`)
  lines.push('')
  lines.push(`Period: ${new Date(r.since).toISOString()} → ${new Date(r.until).toISOString()}`)
  lines.push(
    `Events: ${r.totals.total} (blocked ${r.totals.blocked}, redacted ${r.totals.redacted}, ` +
      `alerted ${r.totals.alerted}, suppressed ${r.totals.suppressed})`,
  )
  lines.push('')
  lines.push('## What changes if we enable block')
  lines.push('')
  lines.push('| Group | Now | Matches | Would block | Keys |')
  lines.push('|---|---|---:|---:|---:|')
  for (const g of groups) {
    const impact = r.would_block.groups[g.id]
    lines.push(
      `| ${g.label} | ${r.default_policy[g.field] || 'off'} | ${impact?.events ?? 0} | ` +
        `${impact?.would_block ?? 0} | ${impact?.keys ?? 0} |`,
    )
  }
  lines.push('')
  lines.push('## By rule')
  lines.push('')
  lines.push('| Rule | Group | Total | Would block | Keys | Last seen |')
  lines.push('|---|---|---:|---:|---:|---|')
  for (const rule of r.rules) {
    lines.push(
      `| ${rule.rule} | ${rule.group} | ${rule.total} | ${rule.would_block} | ${rule.keys} | ` +
        `${new Date(rule.last_seen).toISOString()} |`,
    )
  }
  if (r.keys.length) {
    lines.push('')
    lines.push('## By key')
    lines.push('')
    lines.push('| Key | Policy (secrets/pii/custom) | Total | Would block | Top rule |')
    lines.push('|---|---|---:|---:|---|')
    for (const k of r.keys) {
      lines.push(
        `| ${k.key_id} | ${k.policy.secrets}/${k.policy.pii}/${k.policy.custom} | ${k.total} | ` +
          `${k.would_block} | ${k.top_rule} |`,
      )
    }
  }
  if (r.truncated) {
    lines.push('')
    lines.push('_Truncated: more distinct rule/key/agent combinations than the store returns._')
  }
  return lines.join('\n')
}

const th = { ...colHead, padding: '9px 18px' }
const numCell = { ...mono, fontSize: 12.5, fontVariantNumeric: 'tabular-nums' as const, textAlign: 'right' as const }

export function Report({ toast }: { toast: (msg: string) => void }) {
  const [period, setPeriod] = useState<Period>('7d')
  const [rep, setRep] = useState<AuditReport | null>(null)
  const [loading, setLoading] = useState(true)
  const [disabled, setDisabled] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setRep(await api.dlpReport(period))
      setDisabled(false)
    } catch (e) {
      setRep(null)
      setDisabled(e instanceof ApiError && e.status === 503)
    } finally {
      setLoading(false)
    }
  }, [period])

  useEffect(() => {
    void load()
  }, [load])

  // Clipboard access is refused in some contexts (no focus, insecure origin),
  // so only claim success once the write actually resolves.
  const copyMarkdown = async () => {
    if (!rep) return
    try {
      await navigator.clipboard.writeText(toMarkdown(rep))
      toast('Report copied as Markdown')
    } catch {
      toast('Copy failed — use Download JSON, or allow clipboard access')
    }
  }

  const downloadJSON = () => {
    if (!rep) return
    const blob = new Blob([JSON.stringify(rep, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `aperture-report-${rep.period}.json`
    a.click()
    URL.revokeObjectURL(url)
  }

  const headline = rep
    ? rep.would_block.total > 0
      ? `Switching every detector to block would have rejected ${fmtNum(rep.would_block.total)} of ${fmtNum(rep.totals.total)} flagged requests, across ${rep.would_block.keys} key${rep.would_block.keys === 1 ? '' : 's'}.`
      : rep.totals.total > 0
        ? 'Nothing new would be blocked — everything flagged is already blocked or deliberately muted.'
        : ''
    : ''

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 16, marginBottom: 20 }}>
        <div>
          <h1 style={h1Style}>Audit report</h1>
          <div style={subStyle}>What would have been blocked — before you switch a detector to block</div>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <Segmented value={period} options={periods} onChange={setPeriod} />
          <button
            onClick={() => void copyMarkdown()}
            disabled={!rep}
            className="ap-save-btn"
            style={{ background: 'var(--bg3)', border: '1px solid var(--border2)', color: 'var(--text)', padding: '7px 14px', borderRadius: 7, fontSize: 12.5, fontWeight: 600, cursor: 'pointer' }}
          >
            Copy Markdown
          </button>
          <button
            onClick={downloadJSON}
            disabled={!rep}
            className="ap-save-btn"
            style={{ background: 'var(--bg3)', border: '1px solid var(--border2)', color: 'var(--text)', padding: '7px 14px', borderRadius: 7, fontSize: 12.5, fontWeight: 600, cursor: 'pointer' }}
          >
            Download JSON
          </button>
        </div>
      </div>

      {disabled && (
        <div style={{ ...card, padding: '16px 18px', fontSize: 13, color: 'var(--faint)' }}>
          DLP scanning is disabled, so there is nothing to report. Start the gateway with{' '}
          <span style={{ ...mono, fontSize: 12 }}>DLP_ENABLED=true</span>.
        </div>
      )}

      {loading && !rep && !disabled && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 12 }}>
          <Skeleton height={122} />
          <Skeleton height={122} delay={0.1} />
          <Skeleton height={122} delay={0.2} />
        </div>
      )}

      {rep && rep.totals.total === 0 && (
        <EmptyState
          title="No DLP events in this period"
          sub="Nothing was flagged, so nothing would change. Widen the period or send traffic through the gateway."
        />
      )}

      {rep && rep.totals.total > 0 && (
        <>
          <div style={{ fontSize: 14, marginBottom: 14, lineHeight: 1.5 }}>{headline}</div>

          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 12, marginBottom: 26 }}>
            {groups.map((g) => {
              const impact = rep.would_block.groups[g.id]
              return (
                <ImpactCard
                  key={g.id}
                  label={g.label}
                  current={rep.default_policy[g.field]}
                  events={impact?.events ?? 0}
                  wouldBlock={impact?.would_block ?? 0}
                  keys={impact?.keys ?? 0}
                />
              )
            })}
          </div>

          {rep.truncated && (
            <div style={{ fontSize: 12.5, color: 'var(--amber)', marginBottom: 12 }}>
              More rule/key/agent combinations than the store returns — the tables below understate the tail.
              Totals stay exact.
            </div>
          )}

          <div style={{ ...colHead, marginBottom: 10 }}>By rule</div>
          <div style={{ ...card, overflow: 'hidden', marginBottom: 26 }}>
            <div style={{ display: 'grid', gridTemplateColumns: '1.4fr 0.8fr 90px 90px 110px 70px 110px', gap: '0 10px', borderBottom: '1px solid var(--border)' }}>
              <span style={th}>Rule</span>
              <span style={th}>Group</span>
              <span style={{ ...th, textAlign: 'right' }}>Total</span>
              <span style={{ ...th, textAlign: 'right' }}>Blocked</span>
              <span style={{ ...th, textAlign: 'right' }}>Would block</span>
              <span style={{ ...th, textAlign: 'right' }}>Keys</span>
              <span style={{ ...th, textAlign: 'right' }}>Last seen</span>
            </div>
            {rep.rules.map((r) => (
              <div
                key={r.rule}
                className="ap-row-hover"
                style={{ display: 'grid', gridTemplateColumns: '1.4fr 0.8fr 90px 90px 110px 70px 110px', gap: '0 10px', padding: '11px 18px', borderBottom: '1px solid var(--border)', alignItems: 'center' }}
              >
                <span style={{ ...mono, fontSize: 13 }}>
                  {r.rule}
                  <span style={{ color: 'var(--faint)', fontSize: 11.5, marginLeft: 8 }}>{r.masked_sample}</span>
                </span>
                <span style={{ fontSize: 12.5, color: 'var(--muted)' }}>{r.group}</span>
                <span style={numCell}>{fmtNum(r.total)}</span>
                <span style={numCell}>{fmtNum(r.blocked)}</span>
                <span style={{ ...numCell, color: r.would_block > 0 ? 'var(--red)' : 'var(--faint)', fontWeight: 600 }}>
                  {fmtNum(r.would_block)}
                </span>
                <span style={numCell}>{fmtNum(r.keys)}</span>
                <span style={{ ...numCell, color: 'var(--muted)', fontSize: 12 }}>{timeAgo(r.last_seen)}</span>
              </div>
            ))}
          </div>

          <div style={{ ...colHead, marginBottom: 10 }}>By key</div>
          <div style={{ ...card, overflow: 'hidden', marginBottom: 26 }}>
            <div style={{ display: 'grid', gridTemplateColumns: '1.2fr 1fr 90px 110px 1fr', gap: '0 10px', borderBottom: '1px solid var(--border)' }}>
              <span style={th}>Key</span>
              <span style={th}>Policy (secrets · pii · custom)</span>
              <span style={{ ...th, textAlign: 'right' }}>Total</span>
              <span style={{ ...th, textAlign: 'right' }}>Would block</span>
              <span style={th}>Top rule</span>
            </div>
            {rep.keys.map((k) => (
              <div
                key={k.key_id}
                className="ap-row-hover"
                style={{ display: 'grid', gridTemplateColumns: '1.2fr 1fr 90px 110px 1fr', gap: '0 10px', padding: '11px 18px', borderBottom: '1px solid var(--border)', alignItems: 'center' }}
              >
                <span style={{ ...mono, fontSize: 12.5, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{k.key_id}</span>
                <PolicyCell p={k.policy} />
                <span style={numCell}>{fmtNum(k.total)}</span>
                <span style={{ ...numCell, color: k.would_block > 0 ? 'var(--red)' : 'var(--faint)', fontWeight: 600 }}>
                  {fmtNum(k.would_block)}
                </span>
                <span style={{ ...mono, fontSize: 12.5, color: 'var(--muted)' }}>{k.top_rule}</span>
              </div>
            ))}
          </div>

          {rep.agents.length > 0 && (
            <>
              <div style={{ ...colHead, marginBottom: 10 }}>By agent</div>
              <div style={{ ...card, overflow: 'hidden', marginBottom: 26 }}>
                <div style={{ display: 'grid', gridTemplateColumns: '1.2fr 90px 110px 1fr', gap: '0 10px', borderBottom: '1px solid var(--border)' }}>
                  <span style={th}>Agent</span>
                  <span style={{ ...th, textAlign: 'right' }}>Total</span>
                  <span style={{ ...th, textAlign: 'right' }}>Would block</span>
                  <span style={th}>Top rule</span>
                </div>
                {rep.agents.map((a) => (
                  <div
                    key={a.agent}
                    className="ap-row-hover"
                    style={{ display: 'grid', gridTemplateColumns: '1.2fr 90px 110px 1fr', gap: '0 10px', padding: '11px 18px', borderBottom: '1px solid var(--border)', alignItems: 'center' }}
                  >
                    <span style={{ ...mono, fontSize: 12.5 }}>{a.agent}</span>
                    <span style={numCell}>{fmtNum(a.total)}</span>
                    <span style={{ ...numCell, color: a.would_block > 0 ? 'var(--red)' : 'var(--faint)', fontWeight: 600 }}>
                      {fmtNum(a.would_block)}
                    </span>
                    <span style={{ ...mono, fontSize: 12.5, color: 'var(--muted)' }}>{a.top_rule}</span>
                  </div>
                ))}
              </div>
            </>
          )}

          <div style={{ fontSize: 12, color: 'var(--faint)' }}>
            Would-block counts exclude requests already blocked and matches silenced by a mute or an allowlist
            entry — those stay silenced on purpose.
          </div>
        </>
      )}
    </div>
  )
}
