// The audit log: who changed what in this organization. The feed of incidents
// says what agents did; this says what people did to the rules the agents
// run under — and marks the changes that let more through, because those are
// the entries somebody opens this screen to find.
import { useCallback, useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { audit } from '../api'
import type { AuditEntry, AuditGroup } from '../api'
import { card, colHead, h1Style, mono, subStyle } from './styles'
import { Badge, Segmented } from './ui'
import { Button, Notice } from './forms'
import { fmtTs, timeAgo } from './format'

const PAGE = 50

const GROUPS: { value: AuditGroup | 'all'; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'key', label: 'Keys' },
  { value: 'policy', label: 'Policies' },
  { value: 'limits', label: 'Limits' },
  { value: 'provider', label: 'Providers' },
  { value: 'alerts', label: 'Alerts' },
  { value: 'member', label: 'People' },
  { value: 'token', label: 'Tokens' },
  { value: 'organization', label: 'Organization' },
]

const Name = ({ children }: { children: ReactNode }) => <strong style={{ fontWeight: 600 }}>{children}</strong>

/** What happened, as a sentence whose subject is the actor. */
function describe(e: AuditEntry): ReactNode {
  const m = e.meta ?? {}
  const t = <Name>{e.target}</Name>
  const forKey = m.key_id !== undefined
  switch (e.action) {
    case 'key.create': {
      const own = m.own_provider_keys as string[] | undefined
      return (
        <>
          created key {t}
          {own?.length ? <> with its own {own.join(', ')} credentials</> : null}
        </>
      )
    }
    case 'key.delete':
      return <>deleted key {t}</>
    case 'policy.update':
      return forKey ? <>changed the policy of key {t}</> : <>changed the default policy</>
    case 'policy.reset':
      return <>put key {t} back on the default policy</>
    case 'policy.mute':
      return (
        <>
          muted <Name>{m.rule}</Name> for key {t}
        </>
      )
    case 'policy.unmute':
      return (
        <>
          unmuted <Name>{m.rule}</Name> for key {t}
        </>
      )
    case 'limits.update':
      return forKey ? <>changed the limits of key {t}</> : <>changed the default limits</>
    case 'limits.reset':
      return <>put key {t} back on the default limits</>
    case 'provider.create':
      return <>added provider {t}</>
    case 'provider.update':
      return <>changed provider {t}</>
    case 'provider.delete':
      return <>removed provider {t}</>
    case 'alerts.update':
      return <>changed where alerts go</>
    case 'member.invite':
      return (
        <>
          invited {t} as <Name>{m.role}</Name>
        </>
      )
    case 'member.uninvite':
      return <>withdrew the invitation for {t}</>
    case 'member.join': {
      const via = m.signed_in_with as string | undefined
      return (
        <>
          joined as <Name>{m.role}</Name>
          {via ? <> with {via[0].toUpperCase() + via.slice(1)}</> : null}
        </>
      )
    }
    case 'member.role':
      return <>changed the role of {t}</>
    case 'member.remove':
      return (
        <>
          removed {t} ({String(m.role)})
        </>
      )
    case 'member.leave':
      return <>left the organization</>
    case 'token.create': {
      const scopes = m.scopes as string[] | undefined
      return (
        <>
          created access token {t}
          {scopes?.length ? <> with {scopes.join(', ')}</> : null}
        </>
      )
    }
    case 'token.revoke':
      return <>revoked access token {t}</>
    case 'organization.create':
      return <>created the organization</>
    case 'organization.rename':
      return <>renamed the organization</>
    case 'organization.delete':
      return <>deleted the organization</>
    case 'organization.restore':
      return <>restored the organization</>
  }
  return (
    <>
      <span style={mono}>{e.action}</span> {e.target && t}
    </>
  )
}

function Actor({ e }: { e: AuditEntry }) {
  return (
    <span style={{ display: 'flex', alignItems: 'center', gap: 6, minWidth: 0 }}>
      <span style={{ fontSize: 13, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={e.actor_label}>
        {e.actor_kind === 'operator' ? 'The operator' : e.actor_label}
      </span>
      {e.actor_kind === 'token' && (
        <Badge bg="var(--bg4)" fg="var(--muted)">
          token
        </Badge>
      )}
    </span>
  )
}

export function Audit() {
  const [group, setGroup] = useState<AuditGroup | 'all'>('all')
  const [entries, setEntries] = useState<AuditEntry[] | null>(null)
  const [more, setMore] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async (g: AuditGroup | 'all', before?: number) => {
    const res = await audit.list({ group: g === 'all' ? undefined : g, before, limit: PAGE })
    setMore(res.entries.length === PAGE)
    return res.entries
  }, [])

  useEffect(() => {
    let live = true
    load(group)
      .then((list) => {
        if (!live) return
        setEntries(list)
        setError('')
      })
      .catch((e) => {
        if (!live) return
        setEntries([])
        setError((e as Error).message)
      })
    return () => {
      live = false
    }
  }, [group, load])

  const older = async () => {
    if (!entries?.length) return
    setLoadingMore(true)
    try {
      const next = await load(group, entries[entries.length - 1].id)
      setEntries((cur) => [...(cur ?? []), ...next])
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoadingMore(false)
    }
  }

  const grid = '110px 200px 1fr'

  return (
    <div style={{ maxWidth: 1040 }}>
      <div style={{ marginBottom: 20 }}>
        <h1 style={h1Style}>Audit log</h1>
        <div style={subStyle}>
          Who changed what in this organization. Changes that let more through — a relaxed policy, a muted rule, a
          raised limit — are marked.
        </div>
      </div>

      <div style={{ marginBottom: 12, overflowX: 'auto' }}>
        <Segmented
          value={group}
          options={GROUPS}
          onChange={(g) => {
            setEntries(null)
            setGroup(g)
          }}
        />
      </div>

      {error && (
        <div style={{ marginBottom: 12 }}>
          <Notice>{error}</Notice>
        </div>
      )}

      <div style={{ ...card, overflow: 'hidden' }}>
        <div style={{ display: 'grid', gridTemplateColumns: grid, gap: '0 14px', padding: '9px 18px', borderBottom: '1px solid var(--border)', ...colHead }}>
          <span>When</span>
          <span>Who</span>
          <span>What</span>
        </div>
        {entries === null ? (
          <div style={{ padding: '22px 18px', color: 'var(--faint)', fontSize: 13 }}>Loading…</div>
        ) : entries.length === 0 ? (
          <div style={{ padding: '22px 18px', color: 'var(--faint)', fontSize: 13 }}>
            {group === 'all' ? 'Nothing yet. Changes to keys, policies, providers and people will be listed here.' : 'Nothing in this group yet.'}
          </div>
        ) : (
          entries.map((e) => (
            <div
              key={e.id}
              className="ap-row-hover"
              style={{
                display: 'grid',
                gridTemplateColumns: grid,
                gap: '0 14px',
                padding: '11px 18px',
                borderBottom: '1px solid var(--border)',
                alignItems: 'start',
                boxShadow: e.meta?.weakened ? 'inset 3px 0 0 var(--red)' : undefined,
              }}
            >
              <span style={{ fontSize: 12, color: 'var(--muted)', paddingTop: 1 }} title={fmtTs(e.time)}>
                {timeAgo(e.time)}
              </span>
              <Actor e={e} />
              <span style={{ minWidth: 0 }}>
                <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, flexWrap: 'wrap', fontSize: 13.5 }}>
                  <span>{describe(e)}</span>
                  {e.meta?.weakened && (
                    <Badge bg="var(--red-bg)" fg="var(--red)">
                      weakened
                    </Badge>
                  )}
                </div>
                {e.meta?.changes?.length ? (
                  <ul style={{ ...mono, listStyle: 'none', margin: '5px 0 0', padding: 0, fontSize: 12, color: 'var(--muted)', display: 'flex', flexDirection: 'column', gap: 2 }}>
                    {e.meta.changes.map((c, i) => (
                      <li key={i} style={{ overflowWrap: 'anywhere' }}>
                        {c}
                      </li>
                    ))}
                  </ul>
                ) : null}
                {e.ip && <div style={{ ...mono, fontSize: 11, color: 'var(--faint)', marginTop: 4 }}>from {e.ip}</div>}
              </span>
            </div>
          ))
        )}
      </div>

      {more && (
        <div style={{ marginTop: 12 }}>
          <Button onClick={older} busy={loadingMore}>
            Older entries
          </Button>
        </div>
      )}
    </div>
  )
}
