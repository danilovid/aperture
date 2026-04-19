import { useState, useEffect, useCallback } from 'react'

const API_URL = import.meta.env.VITE_APERTURE_URL || 'http://localhost:8080'

type Period = '24h' | '7d' | '30d'

interface Summary {
  requests: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  cost_usd: number
  avg_latency_ms: number
  error_rate: number
}

interface Bucket {
  ts: string
  requests: number
  total_tokens: number
  cost_usd: number
  avg_latency_ms: number
}

interface ModelStat {
  model: string
  provider: string
  requests: number
  total_tokens: number
  cost_usd: number
  avg_latency_ms: number
}

interface LogEntry {
  ID: string
  Ts: string
  Model: string
  Provider: string
  PromptTokens: number
  CompletionTokens: number
  TotalTokens: number
  CostUSD: number
  LatencyMs: number
  StatusCode: number
  Error: string
}

const PROVIDER_COLORS: Record<string, string> = {
  openai: '#10a37f',
  anthropic: '#d97706',
  groq: '#7c3aed',
}

function fmt(n: number, decimals = 0) {
  return n.toLocaleString('en-US', { maximumFractionDigits: decimals })
}

function fmtCost(n: number) {
  if (n < 0.001) return `$${(n * 1000).toFixed(4)}m`
  return `$${n.toFixed(4)}`
}

function fmtTime(iso: string) {
  return new Date(iso).toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' })
}

// Minimal bar chart via SVG
function BarChart({ data, valueKey, label }: {
  data: Bucket[]
  valueKey: keyof Bucket
  label: string
}) {
  if (!data.length) return <div className="chart-empty">Нет данных</div>

  const values = data.map(d => Number(d[valueKey]))
  const max = Math.max(...values, 1)
  const W = 600
  const H = 80
  const barW = Math.max(2, (W / data.length) - 2)

  return (
    <div className="chart-wrap">
      <div className="chart-label">{label}</div>
      <svg viewBox={`0 0 ${W} ${H}`} className="chart-svg" preserveAspectRatio="none">
        {data.map((d, i) => {
          const h = Math.max(1, (Number(d[valueKey]) / max) * H)
          return (
            <rect
              key={i}
              x={i * (W / data.length)}
              y={H - h}
              width={barW}
              height={h}
              fill="var(--accent)"
              opacity={0.8}
            >
              <title>{`${fmtTime(d.ts)}: ${fmt(Number(d[valueKey]), 2)}`}</title>
            </rect>
          )
        })}
      </svg>
    </div>
  )
}

export function Dashboard() {
  const [period, setPeriod] = useState<Period>('24h')
  const [summary, setSummary] = useState<Summary | null>(null)
  const [buckets, setBuckets] = useState<Bucket[]>([])
  const [models, setModels] = useState<ModelStat[]>([])
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [unavailable, setUnavailable] = useState(false)

  const fetchAll = useCallback(async () => {
    setLoading(true)
    try {
      const bucketHours = period === '24h' ? 1 : period === '7d' ? 6 : 24
      const [sumRes, tsRes, modRes, logsRes] = await Promise.all([
        fetch(`${API_URL}/admin/stats/summary?period=${period}`),
        fetch(`${API_URL}/admin/stats/timeseries?period=${period}&bucket_hours=${bucketHours}`),
        fetch(`${API_URL}/admin/stats/models?period=${period}`),
        fetch(`${API_URL}/admin/stats/logs?limit=50`),
      ])

      if (sumRes.status === 503) {
        setUnavailable(true)
        return
      }
      setUnavailable(false)

      const [sumData, tsData, modData, logsData] = await Promise.all([
        sumRes.json(),
        tsRes.json(),
        modRes.json(),
        logsRes.json(),
      ])

      setSummary(sumData)
      setBuckets(tsData.buckets ?? [])
      setModels(modData.models ?? [])
      setLogs(logsData.logs ?? [])
    } finally {
      setLoading(false)
    }
  }, [period])

  useEffect(() => { fetchAll() }, [fetchAll])

  if (unavailable) {
    return (
      <div className="dash-unavailable">
        <p>Статистика недоступна</p>
        <p className="dash-unavailable-sub">Установите <code>DATABASE_URL</code> для включения мониторинга</p>
      </div>
    )
  }

  return (
    <div className="dash">
      <div className="dash-header">
        <h2 className="dash-title">Мониторинг</h2>
        <div className="dash-period">
          {(['24h', '7d', '30d'] as Period[]).map(p => (
            <button
              key={p}
              className={`dash-period-btn${period === p ? ' active' : ''}`}
              onClick={() => setPeriod(p)}
            >{p}</button>
          ))}
          <button className="dash-refresh" onClick={fetchAll} disabled={loading} title="Обновить">↺</button>
        </div>
      </div>

      {summary && (
        <div className="dash-cards">
          <Card label="Запросов" value={fmt(summary.requests)} />
          <Card label="Токенов" value={fmt(summary.total_tokens)} />
          <Card label="Стоимость" value={fmtCost(summary.cost_usd)} />
          <Card label="Avg latency" value={`${fmt(summary.avg_latency_ms, 0)} ms`} />
          <Card label="Ошибки" value={`${(summary.error_rate * 100).toFixed(1)}%`} />
        </div>
      )}

      <div className="dash-charts">
        <BarChart data={buckets} valueKey="requests" label="Запросы" />
        <BarChart data={buckets} valueKey="cost_usd" label="Стоимость ($)" />
        <BarChart data={buckets} valueKey="avg_latency_ms" label="Latency (ms)" />
      </div>

      {models.length > 0 && (
        <div className="dash-section">
          <h3 className="dash-section-title">По моделям</h3>
          <table className="dash-table">
            <thead>
              <tr>
                <th>Модель</th>
                <th>Провайдер</th>
                <th>Запросов</th>
                <th>Токенов</th>
                <th>Стоимость</th>
                <th>Avg ms</th>
              </tr>
            </thead>
            <tbody>
              {models.map(m => (
                <tr key={m.model}>
                  <td>{m.model}</td>
                  <td>
                    <span className="dash-provider" style={{ background: PROVIDER_COLORS[m.provider] ?? '#555' }}>
                      {m.provider}
                    </span>
                  </td>
                  <td>{fmt(m.requests)}</td>
                  <td>{fmt(m.total_tokens)}</td>
                  <td>{fmtCost(m.cost_usd)}</td>
                  <td>{fmt(m.avg_latency_ms, 0)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {logs.length > 0 && (
        <div className="dash-section">
          <h3 className="dash-section-title">Последние запросы</h3>
          <table className="dash-table dash-table--logs">
            <thead>
              <tr>
                <th>Время</th>
                <th>Модель</th>
                <th>Токены</th>
                <th>Стоимость</th>
                <th>ms</th>
                <th>Статус</th>
              </tr>
            </thead>
            <tbody>
              {logs.map(l => (
                <tr key={l.ID} className={l.StatusCode >= 400 ? 'dash-row--error' : ''}>
                  <td className="dash-ts">{new Date(l.Ts).toLocaleString('ru-RU')}</td>
                  <td>{l.Model}</td>
                  <td>{fmt(l.TotalTokens)}</td>
                  <td>{fmtCost(l.CostUSD)}</td>
                  <td>{l.LatencyMs}</td>
                  <td>
                    <span className={`dash-status dash-status--${l.StatusCode >= 400 ? 'err' : 'ok'}`}>
                      {l.StatusCode}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function Card({ label, value }: { label: string; value: string }) {
  return (
    <div className="dash-card">
      <div className="dash-card-value">{value}</div>
      <div className="dash-card-label">{label}</div>
    </div>
  )
}