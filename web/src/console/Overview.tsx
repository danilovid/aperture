import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import type { DLPSummary, ModelStat, Period, StatsSummary, TimeseriesBucket } from '../api'
import { fmtCost, fmtMs, fmtNum, fmtPct } from './format'
import { ProviderBadge, Segmented, Skeleton } from './ui'
import { card, colHead, h1Style, mono, subStyle } from './styles'

type Metric = 'requests' | 'total_tokens' | 'cost_usd' | 'avg_latency_ms'

function sparkPoints(buckets: TimeseriesBucket[], metric: Metric): string {
  if (buckets.length < 2) return ''
  const vals = buckets.map((b) => b[metric])
  const max = Math.max(...vals, 1)
  const w = 72
  const h = 24
  return vals
    .map((v, i) => `${((i / (vals.length - 1)) * w).toFixed(1)},${(h - 2 - (v / max) * (h - 4)).toFixed(1)}`)
    .join(' ')
}

function Kpi({
  label,
  value,
  sub,
  spark,
  sparkColor = 'var(--accent)',
}: {
  label: string
  value: string
  sub?: string
  spark?: string
  sparkColor?: string
}) {
  return (
    <div style={{ ...card, padding: '16px 18px 14px' }}>
      <div style={{ fontSize: 12.5, color: 'var(--muted)', fontWeight: 500, marginBottom: 6 }}>{label}</div>
      <div style={{ ...mono, fontSize: 22, fontWeight: 600, fontVariantNumeric: 'tabular-nums', letterSpacing: '-0.5px' }}>
        {value}
      </div>
      <div style={{ fontSize: 12, color: 'var(--muted)', margin: '2px 0 10px', minHeight: 18 }}>{sub ?? ''}</div>
      <svg viewBox="0 0 72 24" preserveAspectRatio="none" style={{ width: '100%', height: 24, display: 'block' }}>
        {spark && (
          <polyline
            points={spark}
            fill="none"
            stroke={sparkColor}
            strokeWidth="1.6"
            strokeLinejoin="round"
            strokeLinecap="round"
            vectorEffect="non-scaling-stroke"
          />
        )}
      </svg>
    </div>
  )
}

export function Overview({ period, setPeriod }: { period: Period; setPeriod: (p: Period) => void }) {
  const [summary, setSummary] = useState<StatsSummary | null>(null)
  const [dlp, setDlp] = useState<DLPSummary | null>(null)
  const [buckets, setBuckets] = useState<TimeseriesBucket[]>([])
  const [models, setModels] = useState<ModelStat[]>([])
  const [metric, setMetric] = useState<Metric>('requests')
  const [loading, setLoading] = useState(true)
  const [statsUnavailable, setStatsUnavailable] = useState(false)

  const fetchAll = useCallback(async () => {
    setLoading(true)
    try {
      const bucketHours = period === '24h' ? 1 : period === '7d' ? 6 : 24
      const dlpP = api.dlpSummary(period).catch(() => null)
      try {
        const [s, t, m] = await Promise.all([
          api.statsSummary(period),
          api.statsTimeseries(period, bucketHours),
          api.statsModels(period),
        ])
        setSummary(s)
        setBuckets(t.buckets)
        setModels(m.models)
        setStatsUnavailable(false)
      } catch (e) {
        if (e instanceof ApiError && e.status === 503) {
          setStatsUnavailable(true)
          setSummary(null)
          setBuckets([])
          setModels([])
        } else {
          throw e
        }
      }
      setDlp(await dlpP)
    } finally {
      setLoading(false)
    }
  }, [period])

  useEffect(() => {
    fetchAll()
  }, [fetchAll])

  const maxVal = Math.max(...buckets.map((b) => b[metric]), 1)

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 24, flexWrap: 'wrap', gap: 12 }}>
        <div>
          <h1 style={h1Style}>Overview</h1>
          <div style={subStyle}>Gateway traffic across all keys and providers</div>
        </div>
        <Segmented
          value={period}
          onChange={setPeriod}
          monoFont
          options={[
            { value: '24h', label: '24h' },
            { value: '7d', label: '7d' },
            { value: '30d', label: '30d' },
          ]}
        />
      </div>

      {statsUnavailable && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, background: 'var(--amber-bg)', border: '1px solid var(--amber)', borderRadius: 10, padding: '12px 16px', marginBottom: 22, fontSize: 13 }}>
          <span style={{ width: 8, height: 8, borderRadius: '50%', background: 'var(--amber)', display: 'inline-block', flexShrink: 0 }} />
          <span>
            <strong style={{ color: 'var(--amber)' }}>Request stats unavailable.</strong> Set{' '}
            <span style={{ ...mono, fontSize: 12 }}>DATABASE_URL</span> to enable traffic monitoring. DLP counters below still work.
          </span>
        </div>
      )}

      {loading ? (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: 14, marginBottom: 22 }}>
          {[0, 1, 2, 3, 4, 5].map((i) => (
            <Skeleton key={i} height={118} delay={i * 0.1} />
          ))}
        </div>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: 14, marginBottom: 22 }}>
          <Kpi label="Requests" value={summary ? fmtNum(summary.requests) : '—'} spark={sparkPoints(buckets, 'requests')} />
          <Kpi
            label="DLP events"
            value={dlp ? String(dlp.total) : '—'}
            sub={dlp ? `${dlp.blocked} blocked · ${dlp.redacted} redacted` : undefined}
            sparkColor="var(--red)"
          />
          <Kpi label="Total tokens" value={summary ? fmtNum(summary.total_tokens) : '—'} spark={sparkPoints(buckets, 'total_tokens')} />
          <Kpi label="Cost" value={summary ? fmtCost(summary.cost_usd) : '—'} spark={sparkPoints(buckets, 'cost_usd')} sparkColor="var(--amber)" />
          <Kpi label="Avg latency" value={summary ? fmtMs(summary.avg_latency_ms) : '—'} spark={sparkPoints(buckets, 'avg_latency_ms')} />
          <Kpi label="Error rate" value={summary ? fmtPct(summary.error_rate) : '—'} />
        </div>
      )}

      <div style={{ ...card, padding: '20px 22px', marginBottom: 22 }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 18, flexWrap: 'wrap', gap: 10 }}>
          <div style={{ fontWeight: 600, fontSize: 14.5 }}>Traffic over time</div>
          <Segmented
            value={metric}
            onChange={setMetric}
            options={[
              { value: 'requests', label: 'Requests' },
              { value: 'total_tokens', label: 'Tokens' },
              { value: 'cost_usd', label: 'Cost' },
              { value: 'avg_latency_ms', label: 'Latency' },
            ]}
          />
        </div>
        {buckets.length === 0 ? (
          <div style={{ height: 190, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--faint)', fontSize: 13 }}>
            No traffic data{statsUnavailable ? ' — requires PostgreSQL' : ' for this period yet'}
          </div>
        ) : (
          <>
            <div style={{ display: 'flex', alignItems: 'flex-end', gap: 3, height: 190 }}>
              {buckets.map((b, i) => (
                <div
                  key={i}
                  className="ap-bar"
                  title={`${new Date(b.ts).toLocaleString()}: ${b[metric]}`}
                  style={{
                    flex: 1,
                    height: `${Math.max((b[metric] / maxVal) * 100, 1)}%`,
                    minHeight: 2,
                    background: 'var(--accent)',
                    opacity: 0.8,
                    borderRadius: '3px 3px 0 0',
                  }}
                />
              ))}
            </div>
            <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 8, ...mono, fontSize: 11, color: 'var(--faint)' }}>
              <span>{buckets.length ? new Date(buckets[0].ts).toLocaleDateString() : ''}</span>
              <span>now</span>
            </div>
          </>
        )}
      </div>

      <div style={{ ...card, overflow: 'hidden' }}>
        <div style={{ padding: '16px 22px 12px', fontWeight: 600, fontSize: 14.5 }}>By model</div>
        <div style={{ display: 'grid', gridTemplateColumns: '2.2fr 1fr 1fr 1fr 1fr', gap: '0 16px', padding: '8px 22px', borderBottom: '1px solid var(--border)', ...colHead }}>
          <span>Model</span>
          <span style={{ textAlign: 'right' }}>Requests</span>
          <span style={{ textAlign: 'right' }}>Tokens</span>
          <span style={{ textAlign: 'right' }}>Cost</span>
          <span style={{ textAlign: 'right' }}>Avg latency</span>
        </div>
        {models.length === 0 ? (
          <div style={{ padding: '28px 22px', color: 'var(--faint)', fontSize: 13 }}>
            No per-model data{statsUnavailable ? ' — requires PostgreSQL' : ' yet'}
          </div>
        ) : (
          models.map((m) => (
            <div
              key={m.model}
              className="ap-row-hover"
              style={{ display: 'grid', gridTemplateColumns: '2.2fr 1fr 1fr 1fr 1fr', gap: '0 16px', padding: '12px 22px', borderBottom: '1px solid var(--border)', alignItems: 'center' }}
            >
              <span style={{ display: 'flex', alignItems: 'center', gap: 10, minWidth: 0 }}>
                <ProviderBadge provider={m.provider} />
                <span style={{ ...mono, fontSize: 13, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{m.model}</span>
              </span>
              <span style={{ ...mono, fontSize: 13, textAlign: 'right', fontVariantNumeric: 'tabular-nums' }}>{fmtNum(m.requests)}</span>
              <span style={{ ...mono, fontSize: 13, textAlign: 'right', fontVariantNumeric: 'tabular-nums' }}>{fmtNum(m.total_tokens)}</span>
              <span style={{ ...mono, fontSize: 13, textAlign: 'right', fontVariantNumeric: 'tabular-nums' }}>{fmtCost(m.cost_usd)}</span>
              <span style={{ ...mono, fontSize: 13, textAlign: 'right', fontVariantNumeric: 'tabular-nums', color: 'var(--muted)' }}>{fmtMs(m.avg_latency_ms)}</span>
            </div>
          ))
        )}
      </div>
    </div>
  )
}
