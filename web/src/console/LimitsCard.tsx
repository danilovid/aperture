import { useCallback, useEffect, useState } from 'react'
import { api } from '../api'
import type { Limits, LimitsResponse } from '../api'
import { fmtCost } from './format'
import { card, mono } from './styles'

const numInput = {
  background: 'var(--bg)',
  border: '1px solid var(--border)',
  borderRadius: 7,
  padding: '7px 10px',
  fontSize: 13,
  fontFamily: "'IBM Plex Mono', monospace",
  color: 'var(--text)',
  width: 110,
  textAlign: 'right' as const,
}


/** One editable row: the default ceiling or a single key's. */
export function LimitRow({
  label,
  hint,
  value,
  spent,
  onSave,
  onClear,
  saving,
  hideLabel,
}: {
  label: string
  hint?: string
  value: Limits
  spent?: number
  onSave: (l: Limits) => void
  onClear?: () => void
  saving: boolean
  /** Where the surroundings already say whose limit this is. */
  hideLabel?: boolean
}) {
  // The parent keys this row by its saved values, so a reload remounts it
  // with fresh defaults instead of syncing props into state.
  const [budget, setBudget] = useState(String(value.budget_daily_usd ?? ''))
  const [rpm, setRpm] = useState(String(value.requests_per_minute ?? ''))

  const budgetNum = Number(budget) || 0
  const overspent = spent !== undefined && budgetNum > 0 && spent >= budgetNum

  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        flexWrap: 'wrap',
        padding: '11px 16px',
        borderBottom: '1px solid var(--border)',
      }}
    >
      {!hideLabel && <span style={{ ...mono, fontSize: 13, minWidth: 140 }}>{label}</span>}
      <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--muted)' }}>
        $/day
        <input
          type="number"
          min="0"
          step="0.01"
          value={budget}
          onChange={(e) => setBudget(e.target.value)}
          placeholder="∞"
          aria-label={`${label} daily budget in USD`}
          style={numInput}
        />
      </label>
      <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--muted)' }}>
        req/min
        <input
          type="number"
          min="0"
          step="1"
          value={rpm}
          onChange={(e) => setRpm(e.target.value)}
          placeholder="∞"
          aria-label={`${label} requests per minute`}
          style={numInput}
        />
      </label>
      {spent !== undefined && (
        <span
          style={{
            ...mono,
            fontSize: 12,
            color: overspent ? 'var(--red)' : 'var(--muted)',
            minWidth: 116,
          }}
          title="Spent today (UTC)"
        >
          {fmtCost(spent)} today{overspent ? ' · cut off' : ''}
        </span>
      )}
      <div style={{ flex: 1 }} />
      <button
        onClick={() =>
          onSave({
            budget_daily_usd: Number(budget) || 0,
            requests_per_minute: Number(rpm) || 0,
          })
        }
        disabled={saving}
        className="ap-save-btn"
        style={{
          background: 'var(--bg3)',
          border: '1px solid var(--border2)',
          color: 'var(--text)',
          padding: '6px 14px',
          borderRadius: 7,
          fontSize: 12.5,
          fontWeight: 600,
          cursor: 'pointer',
        }}
      >
        Save
      </button>
      {onClear && (
        <button
          onClick={onClear}
          className="ap-danger-btn"
          style={{
            background: 'none',
            border: 'none',
            color: 'var(--faint)',
            fontSize: 12.5,
            cursor: 'pointer',
            padding: '4px 8px',
            borderRadius: 5,
          }}
          title="Fall back to the default"
        >
          Reset
        </button>
      )}
      {hint && (
        <div style={{ flexBasis: '100%', fontSize: 12, color: 'var(--faint)', marginTop: 2 }}>{hint}</div>
      )}
    </div>
  )
}

/**
 * The ceiling every key has unless it sets its own. A key's own ceiling is
 * set on the key itself, under API keys — one place per key, rather than a
 * row per key here that grows with every key anybody creates.
 */
export function DefaultLimits({ toast }: { toast: (msg: string) => void }) {
  const [data, setData] = useState<LimitsResponse | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try {
      setData(await api.limits())
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const save = async (l: Limits) => {
    setSaving(true)
    try {
      await api.putDefaultLimits(l)
      toast('Default limits saved')
      await load()
    } catch (e) {
      toast(`Save failed: ${(e as Error).message}`)
    } finally {
      setSaving(false)
    }
  }

  if (error) return <div style={{ ...card, padding: '16px 18px', fontSize: 13, color: 'var(--faint)' }}>{error}</div>
  if (!data) return null
  return (
    <div style={{ ...card, overflow: 'hidden' }}>
      <LimitRow
        key={`default:${data.default.budget_daily_usd ?? ''}:${data.default.requests_per_minute ?? ''}`}
        label="Every key"
        hint="Empty means no limit. Budgets reset at 00:00 UTC."
        value={data.default}
        onSave={save}
        saving={saving}
      />
    </div>
  )
}
