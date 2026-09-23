// Settings: everything that is set up once and then left alone, away from the
// screens people work in every day. A menu of its own on the left, one page
// per concern on the right — the gateway's keys, providers, limits and alerts,
// and the organization's people and records.
import { useCallback, useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { api, ApiError } from '../api'
import type { Me } from '../api'
import { getAdminKey, getMutegateKey, setAdminKey, setMutegateKey } from '../auth'
import { Link, navigate } from '../router'
import { ApiKeys } from './ApiKeys'
import { ProvidersCard } from './ProvidersCard'
import { DefaultLimits } from './LimitsCard'
import { AlertsCard } from './AlertsCard'
import { Organization } from './Organization'
import { Members } from './Members'
import { Tokens } from './Tokens'
import { Audit } from './Audit'
import { Badge } from './ui'
import { card, h1Style, mono, provStyle, subStyle } from './styles'
import { Notice } from './forms'
import { settingsSections } from './settingsSections'

export function SettingsArea({
  section,
  me,
  onMe,
  onSignedOut,
  noDB,
  toast,
}: {
  section?: string
  me?: Me
  onMe?: (me: Me) => void
  onSignedOut: () => void
  noDB: boolean
  toast: (msg: string) => void
}) {
  const allowed = settingsSections(me)
  const current = allowed.find((s) => s.id === section) ?? allowed[0]

  // /app/settings on its own, or a page this role cannot open, lands on the
  // first one it can.
  useEffect(() => {
    if (current && current.id !== section) navigate(`/app/settings/${current.id}`, { replace: true })
  }, [current, section])

  if (!current) return null
  const groups = ['Gateway', 'Organization'] as const

  return (
    <div style={{ display: 'flex', gap: 32, alignItems: 'flex-start' }}>
      <nav aria-label="Settings" style={{ width: 176, flexShrink: 0, position: 'sticky', top: 28, display: 'flex', flexDirection: 'column', gap: 1 }}>
        <div className="ap-display" style={{ fontSize: 20, padding: '0 10px 14px' }}>
          Settings
        </div>
        {groups.map((g) => {
          const items = allowed.filter((s) => s.group === g)
          if (items.length === 0) return null
          return (
            <div key={g} style={{ display: 'flex', flexDirection: 'column', gap: 1, marginBottom: 14 }}>
              <div style={{ fontSize: 12, color: 'var(--faint)', fontWeight: 600, padding: '0 10px 4px' }}>{g}</div>
              {items.map((s) => {
                const active = s.id === current.id
                return (
                  <Link
                    key={s.id}
                    to={`/app/settings/${s.id}`}
                    className="ap-nav-btn"
                    aria-current={active ? 'page' : undefined}
                    style={{ padding: '6px 10px', borderRadius: 7, fontSize: 13.5, fontWeight: active ? 600 : 500, color: active ? 'var(--text)' : 'var(--muted)', background: active ? 'var(--bg4)' : 'none' }}
                  >
                    {s.label}
                  </Link>
                )
              })}
            </div>
          )
        })}
      </nav>

      <div style={{ flex: 1, minWidth: 0 }}>
        {current.id === 'access' && <ConsoleAccess />}
        {current.id === 'keys' && <ApiKeys noDB={noDB} toast={toast} />}
        {current.id === 'providers' && <Providers toast={toast} />}
        {current.id === 'limits' && (
          <Page title="Limits" sub="The daily budget and request rate every key gets unless it has its own. A key's own limit is set on the key, under API keys.">
            <DefaultLimits toast={toast} />
          </Page>
        )}
        {current.id === 'alerts' && (
          <Page title="Alerts" sub="Where incidents are sent as they happen: a webhook, Slack or Telegram.">
            <AlertsCard toast={toast} />
          </Page>
        )}
        {current.id === 'general' && me && onMe && <Organization me={me} onMe={onMe} onSignedOut={onSignedOut} toast={toast} />}
        {current.id === 'members' && me && <Members me={me} toast={toast} />}
        {current.id === 'tokens' && <Tokens toast={toast} />}
        {current.id === 'audit' && <Audit />}
      </div>
    </div>
  )
}

function Page({ title, sub, children }: { title: string; sub: string; children: ReactNode }) {
  return (
    <div style={{ maxWidth: 820 }}>
      <div style={{ marginBottom: 20 }}>
        <h1 style={h1Style}>{title}</h1>
        <div style={subStyle}>{sub}</div>
      </div>
      {children}
    </div>
  )
}

/**
 * The organization's upstreams. With a database they are records with
 * addresses, proxies and a connectivity check; without one there is only this
 * gateway's runtime key per provider, and the old form stays for that case.
 */
function Providers({ toast }: { toast: (msg: string) => void }) {
  const [legacy, setLegacy] = useState(false)
  const fallBack = useCallback(() => setLegacy(true), [])
  return (
    <Page title="Providers" sub="Where requests go, with which key, and which way out. A provider's key is the default for every API key in the organization.">
      {legacy ? <LegacyProviderKeys toast={toast} /> : <ProvidersCard toast={toast} onUnavailable={fallBack} />}
    </Page>
  )
}

const inputStyle = {
  background: 'var(--bg)',
  border: '1px solid var(--border)',
  borderRadius: 7,
  padding: '8px 12px',
  fontSize: 13,
  fontFamily: 'var(--mono)',
  color: 'var(--text)',
} as const

const legacyProviders = [
  { id: 'openai', name: 'OpenAI', field: 'openai_api_key', placeholder: 'sk-proj-…' },
  { id: 'anthropic', name: 'Anthropic', field: 'anthropic_api_key', placeholder: 'sk-ant-…' },
  { id: 'groq', name: 'Groq', field: 'groq_api_key', placeholder: 'gsk_…' },
  // Not an LLM: the Jev decision API, fronted so its business fields are
  // scanned like any other outbound traffic.
  { id: 'jev', name: 'Jev', field: 'jev_api_key', placeholder: 'key from jevai.org/agent/keys' },
] as const

/** Provider keys on a gateway without a database: one per provider, in memory. */
function LegacyProviderKeys({ toast }: { toast: (msg: string) => void }) {
  const [configured, setConfigured] = useState<string[]>([])
  const [vals, setVals] = useState<Record<string, string>>({})

  const load = useCallback(async () => {
    try {
      const cfg = await api.config()
      setConfigured(cfg.configured_providers ?? [])
    } catch {
      setConfigured([])
    }
  }, [])

  useEffect(() => {
    // It only sets state once the request has answered.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load()
  }, [load])

  const save = async (field: string, id: string) => {
    const v = vals[id]?.trim()
    if (!v) return
    try {
      await api.setConfig({ [field]: v })
      setVals((s) => ({ ...s, [id]: '' }))
      toast(`${id} key saved`)
      void load()
    } catch (e) {
      toast(`Save failed: ${(e as Error).message}`)
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <Notice tone="warn">
        Running without a database: these keys live in memory and are lost on restart. Set DATABASE_URL to keep them.
      </Notice>
      {legacyProviders.map((p) => {
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
              onClick={() => save(p.field, p.id)}
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
  )
}

/**
 * Without accounts the console signs its requests with the admin key, and the
 * playground with a Mutegate key; both are pasted here from the gateway's
 * startup log.
 */
function ConsoleAccess() {
  const [admin, setAdmin] = useState(getAdminKey)
  const [mutegate, setMutegate] = useState(getMutegateKey)
  const [unauthorized, setUnauthorized] = useState(false)

  const check = useCallback(async () => {
    try {
      await api.config()
      setUnauthorized(false)
    } catch (e) {
      setUnauthorized(e instanceof ApiError && e.status === 401)
    }
  }, [])

  useEffect(() => {
    // It only sets state once the request has answered.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void check()
  }, [check])

  const row = { display: 'flex', alignItems: 'center', gap: 14 } as const
  const label = { fontSize: 12.5, color: 'var(--muted)', width: 130, flexShrink: 0 } as const
  return (
    <Page title="Console access" sub="The keys this browser uses to talk to the gateway, from its log at startup.">
      <div style={{ ...card, padding: '16px 18px', display: 'flex', flexDirection: 'column', gap: 10 }}>
        {unauthorized && <Notice>Unauthorized — paste the admin API key from the gateway's startup log.</Notice>}
        <div style={row}>
          <span style={label}>Admin API key</span>
          <input
            type="password"
            value={admin}
            onChange={(e) => {
              setAdmin(e.target.value)
              setAdminKey(e.target.value)
            }}
            onBlur={check}
            placeholder="admin-… (from the gateway's log)"
            aria-label="Admin API key"
            style={{ ...inputStyle, flex: 1 }}
          />
        </div>
        <div style={row}>
          <span style={label}>Mutegate API key</span>
          <input
            type="password"
            value={mutegate}
            onChange={(e) => {
              setMutegate(e.target.value)
              setMutegateKey(e.target.value)
            }}
            placeholder="ap-… (used by the playground)"
            aria-label="Mutegate API key"
            style={{ ...inputStyle, flex: 1 }}
          />
        </div>
        <div style={{ ...mono, fontSize: 11.5, color: 'var(--faint)' }}>Kept in this browser only.</div>
      </div>
    </Page>
  )
}
