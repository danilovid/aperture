import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../api'
import type { AlertConfig, AlertFormat } from '../api'
import { card, colHead, mono } from './styles'
import { Segmented } from './ui'

const inputStyle = {
  background: 'var(--bg)',
  border: '1px solid var(--border)',
  borderRadius: 7,
  padding: '8px 12px',
  fontSize: 13,
  fontFamily: "'IBM Plex Mono', monospace",
  color: 'var(--text)',
} as const

const labelStyle = { fontSize: 12.5, color: 'var(--muted)', width: 130, flexShrink: 0 } as const
const rowStyle = { display: 'flex', alignItems: 'center', gap: 14 } as const

const formats: { value: AlertFormat; label: string }[] = [
  { value: 'json', label: 'JSON' },
  { value: 'slack', label: 'Slack' },
  { value: 'telegram', label: 'Telegram' },
]

// Every action the DLP engine records; empty selection means blocked only.
const actions = ['blocked', 'redacted', 'alerted', 'suppressed'] as const

const placeholders: Record<AlertFormat, string> = {
  json: 'https://example.com/hooks/aperture',
  slack: 'https://hooks.slack.com/services/T…/B…/…',
  telegram: 'https://api.telegram.org/bot<token>/sendMessage',
}

/** Webhook alert setup: destination, format, which actions fire, debounce. */
export function AlertsCard({ toast }: { toast: (msg: string) => void }) {
  const [cfg, setCfg] = useState<AlertConfig | null>(null)
  // The gateway returns the URL masked, so an untouched field must be sent back
  // as-is (the backend then keeps the stored URL) rather than cleared.
  const [maskedURL, setMaskedURL] = useState('')
  const [disabled, setDisabled] = useState(false)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)

  const load = useCallback(async () => {
    try {
      const c = await api.alerts()
      setCfg({ ...c, format: c.format || 'json', actions: c.actions ?? [] })
      setMaskedURL(c.url)
      setDisabled(false)
    } catch (e) {
      // 503 means the gateway runs with DLP off, so there is nothing to alert on.
      if (e instanceof ApiError && e.status === 503) setDisabled(true)
      setCfg(null)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  if (disabled) {
    return (
      <>
        <div style={{ ...colHead, marginBottom: 10 }}>Webhook alerts</div>
        <div style={{ ...card, padding: '16px 18px', marginBottom: 30, fontSize: 13, color: 'var(--faint)' }}>
          Alerting is off because DLP scanning is disabled. Start the gateway with{' '}
          <span style={{ ...mono, fontSize: 12 }}>DLP_ENABLED=true</span> to configure a webhook.
        </div>
      </>
    )
  }
  if (!cfg) return null

  const patch = (p: Partial<AlertConfig>) => setCfg((c) => (c ? { ...c, ...p } : c))

  const toggleAction = (a: string) => {
    const cur = cfg.actions ?? []
    patch({ actions: cur.includes(a) ? cur.filter((x) => x !== a) : [...cur, a] })
  }

  const save = async () => {
    if (cfg.format === 'telegram' && !cfg.chat_id?.trim()) {
      toast('Telegram needs a chat_id')
      return
    }
    setSaving(true)
    try {
      await api.putAlerts({ ...cfg, url: cfg.url.trim(), chat_id: cfg.chat_id?.trim() })
      toast(cfg.url.trim() ? 'Alert config saved' : 'Alerting disabled (no URL)')
      await load()
    } catch (e) {
      toast(`Save failed: ${(e as Error).message}`)
    } finally {
      setSaving(false)
    }
  }

  const sendTest = async () => {
    setTesting(true)
    try {
      await api.testAlert()
      toast('Test alert delivered')
    } catch (e) {
      toast(`Test failed: ${(e as Error).message}`)
    } finally {
      setTesting(false)
    }
  }

  const urlUnchanged = cfg.url === maskedURL && maskedURL !== ''

  return (
    <>
      <div style={{ ...colHead, marginBottom: 10 }}>Webhook alerts</div>
      <div style={{ ...card, padding: '16px 18px', marginBottom: 30, display: 'flex', flexDirection: 'column', gap: 12 }}>
        <div style={rowStyle}>
          <span style={labelStyle}>Webhook URL</span>
          <input
            value={cfg.url}
            onChange={(e) => patch({ url: e.target.value })}
            placeholder={placeholders[cfg.format]}
            aria-label="Webhook URL"
            style={{ ...inputStyle, flex: 1 }}
          />
        </div>
        {urlUnchanged && (
          <div style={{ fontSize: 12, color: 'var(--faint)', marginLeft: 144, marginTop: -6 }}>
            Masked for safety — leave it as is to keep the current URL, or paste a new one to replace it.
          </div>
        )}

        <div style={rowStyle}>
          <span style={labelStyle}>Format</span>
          <Segmented value={cfg.format} options={formats} onChange={(f) => patch({ format: f })} />
        </div>

        {cfg.format === 'telegram' && (
          <div style={rowStyle}>
            <span style={labelStyle}>Chat ID</span>
            <input
              value={cfg.chat_id ?? ''}
              onChange={(e) => patch({ chat_id: e.target.value })}
              placeholder="-1001234567890"
              aria-label="Telegram chat ID"
              style={{ ...inputStyle, flex: '0 0 240px' }}
            />
          </div>
        )}

        <div style={{ ...rowStyle, flexWrap: 'wrap' }}>
          <span style={labelStyle}>Alert on</span>
          {actions.map((a) => (
            <label key={a} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12.5, color: 'var(--text)', cursor: 'pointer' }}>
              <input
                type="checkbox"
                checked={(cfg.actions ?? []).includes(a)}
                onChange={() => toggleAction(a)}
                aria-label={`Alert on ${a}`}
                style={{ accentColor: 'var(--accent)', cursor: 'pointer' }}
              />
              {a}
            </label>
          ))}
          {(cfg.actions ?? []).length === 0 && (
            <span style={{ fontSize: 12, color: 'var(--faint)' }}>none selected → blocked only</span>
          )}
        </div>

        <div style={rowStyle}>
          <span style={labelStyle}>Debounce</span>
          <input
            type="number"
            min="0"
            step="1"
            value={cfg.debounce_seconds || ''}
            onChange={(e) => patch({ debounce_seconds: Number(e.target.value) || 0 })}
            placeholder="60"
            aria-label="Debounce seconds"
            style={{ ...inputStyle, width: 90, textAlign: 'right' }}
          />
          <span style={{ fontSize: 12, color: 'var(--faint)' }}>
            seconds of silence per key + rule, so a looping agent sends one alert, not a thousand
          </span>
        </div>

        <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 4 }}>
          <button
            onClick={save}
            disabled={saving}
            className="ap-accent-btn"
            style={{ background: 'var(--accent)', color: '#0b0e13', border: 'none', padding: '8px 18px', borderRadius: 7, fontSize: 13, fontWeight: 600, cursor: 'pointer' }}
          >
            Save
          </button>
          <button
            onClick={sendTest}
            disabled={testing || !cfg.url.trim()}
            className="ap-save-btn"
            style={{ background: 'var(--bg3)', border: '1px solid var(--border2)', color: 'var(--text)', padding: '8px 16px', borderRadius: 7, fontSize: 12.5, fontWeight: 600, cursor: cfg.url.trim() ? 'pointer' : 'not-allowed' }}
            title={cfg.url.trim() ? 'Deliver a synthetic blocked event' : 'Set a webhook URL first'}
          >
            {testing ? 'Sending…' : 'Send test alert'}
          </button>
          <span style={{ fontSize: 12, color: 'var(--faint)' }}>
            Sends a sample <span style={{ ...mono, fontSize: 11.5 }}>aws-access-key</span> block using the saved config.
          </span>
        </div>
      </div>
    </>
  )
}
