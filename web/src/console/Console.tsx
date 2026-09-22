import { useCallback, useEffect, useState } from 'react'
import './theme.css'
import { api, ApiError, auth, atLeast } from '../api'
import type { Me, Period, Role } from '../api'
import { navigate, Link } from '../router'
import type { Theme } from '../theme'
import { Logo } from './ui'
import { mono } from './styles'
import { Overview } from './Overview'
import { DlpEvents } from './DlpEvents'
import { Policies } from './Policies'
import { Report } from './Report'
import { Settings } from './Settings'
import { Members } from './Members'
import { Tokens } from './Tokens'
import { Organization } from './Organization'
import ChatApp from '../App'

type Screen =
  | 'overview'
  | 'events'
  | 'policies'
  | 'report'
  | 'settings'
  | 'playground'
  | 'members'
  | 'tokens'
  | 'organization'

interface NavItem {
  id: Screen
  label: string
  /** Hidden below this role in a signed-in console. */
  min: Role
  /** Only exists where there are accounts. */
  accountsOnly?: boolean
}

// The server decides what a role may do; the sidebar only avoids showing a
// screen whose every request would be refused.
const traffic: NavItem[] = [
  { id: 'overview', label: 'Overview', min: 'viewer' },
  { id: 'events', label: 'DLP Events', min: 'viewer' },
  { id: 'policies', label: 'Policies', min: 'viewer' },
  { id: 'report', label: 'Report', min: 'viewer' },
  { id: 'settings', label: 'Settings & Keys', min: 'admin' },
  { id: 'playground', label: 'Playground', min: 'member' },
]
const organization: NavItem[] = [
  { id: 'members', label: 'Members', min: 'viewer', accountsOnly: true },
  { id: 'tokens', label: 'Access tokens', min: 'admin', accountsOnly: true },
  { id: 'organization', label: 'Organization', min: 'viewer', accountsOnly: true },
]

interface Toast {
  id: number
  msg: string
}

/** The screen a path names: /app/<screen>, anything else is the overview. */
function screenOf(path: string, allowed: NavItem[]): Screen {
  const id = path.split('/')[2] as Screen | undefined
  return allowed.some((n) => n.id === id) ? (id as Screen) : 'overview'
}

export function Console({
  theme,
  toggleTheme,
  path,
  me,
  onMe,
  onSignOut,
}: {
  theme: Theme
  toggleTheme: () => void
  path: string
  /** Absent on an installation without accounts. */
  me?: Me
  onMe?: (me: Me) => void
  onSignOut?: (everywhere?: boolean) => Promise<void>
}) {
  const accounts = !!me
  const visible = (items: NavItem[]) =>
    items.filter((n) => (accounts ? atLeast(me.role, n.min) : !n.accountsOnly))
  const trafficNav = visible(traffic)
  const orgNav = visible(organization)
  const screen = screenOf(path, [...trafficNav, ...orgNav])

  const [period, setPeriod] = useState<Period>('24h')
  const [blockedBadge, setBlockedBadge] = useState(0)
  const [noDB, setNoDB] = useState(false)
  const [toasts, setToasts] = useState<Toast[]>([])

  const toast = useCallback((msg: string) => {
    const id = Date.now() + Math.random()
    setToasts((t) => [...t, { id, msg }])
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 3500)
  }, [])

  // Sidebar badge + no-DB probe; refreshed on screen switch.
  useEffect(() => {
    api
      .dlpSummary('24h')
      .then((s) => setBlockedBadge(s.blocked))
      .catch(() => setBlockedBadge(0))
    api
      .statsSummary('24h')
      .then(() => setNoDB(false))
      .catch((e) => setNoDB(e instanceof ApiError && e.status === 503))
  }, [screen])

  const switchOrg = async (orgID: string) => {
    try {
      const next = await auth.switchOrg(orgID)
      onMe?.(next)
      navigate('/app/overview')
    } catch (e) {
      toast((e as Error).message)
    }
  }

  const signOut = async (everywhere = false) => {
    await onSignOut?.(everywhere)
    navigate('/login', { replace: true })
  }

  const navButton = (n: NavItem) => (
    <Link
      key={n.id}
      to={`/app/${n.id}`}
      className="ap-nav-btn"
      aria-current={screen === n.id ? 'page' : undefined}
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 8,
        background: screen === n.id ? 'var(--bg3)' : 'none',
        color: screen === n.id ? 'var(--accent)' : 'var(--text)',
        padding: '8px 10px',
        borderRadius: 7,
        fontSize: 13.5,
        fontWeight: 500,
      }}
    >
      <span>{n.label}</span>
      {n.id === 'events' && blockedBadge > 0 && (
        <span style={{ ...mono, fontSize: 11, background: 'var(--red-bg)', color: 'var(--red)', padding: '1px 7px', borderRadius: 99 }}>
          {blockedBadge}
        </span>
      )}
    </Link>
  )

  return (
    <div className="ap-root" data-ap-theme={theme}>
      <div style={{ display: 'flex', minHeight: '100vh' }}>
        {/* sidebar */}
        <div style={{ width: 224, flexShrink: 0, background: 'var(--bg2)', borderRight: '1px solid var(--border)', display: 'flex', flexDirection: 'column', padding: '18px 12px', position: 'sticky', top: 0, height: '100vh', boxSizing: 'border-box', overflowY: 'auto' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 9, padding: '4px 10px 14px' }}>
            <Logo />
            <span style={{ fontWeight: 700, fontSize: 15.5 }}>Aperture</span>
          </div>

          {me?.organization && (
            <OrgSwitcher me={me} onSwitch={switchOrg} />
          )}

          <nav aria-label="Traffic" style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            {trafficNav.map(navButton)}
          </nav>

          {orgNav.length > 0 && (
            <nav aria-label="Organization" style={{ display: 'flex', flexDirection: 'column', gap: 2, marginTop: 18 }}>
              <div style={{ fontSize: 11, color: 'var(--faint)', fontWeight: 600, letterSpacing: 0.6, textTransform: 'uppercase', padding: '0 10px 6px' }}>
                Organization
              </div>
              {orgNav.map(navButton)}
            </nav>
          )}

          <div style={{ flex: 1 }} />
          <div style={{ display: 'flex', flexDirection: 'column', gap: 2, borderTop: '1px solid var(--border)', paddingTop: 12 }}>
            {me && (
              <div style={{ padding: '4px 10px 8px', minWidth: 0 }}>
                <div style={{ fontSize: 13, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {me.user.name || me.user.email}
                </div>
                {me.user.name && (
                  <div style={{ ...mono, fontSize: 11.5, color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {me.user.email}
                  </div>
                )}
              </div>
            )}
            <button
              onClick={toggleTheme}
              className="ap-ghost-btn"
              style={{ background: 'none', border: 'none', textAlign: 'left', padding: '8px 10px', borderRadius: 7, fontSize: 13, color: 'var(--muted)', cursor: 'pointer' }}
            >
              {theme === 'dark' ? '☀ Light theme' : '☾ Dark theme'}
            </button>
            {me && (
              <button
                onClick={() => signOut()}
                className="ap-ghost-btn"
                style={{ background: 'none', border: 'none', textAlign: 'left', padding: '8px 10px', borderRadius: 7, fontSize: 13, color: 'var(--muted)', cursor: 'pointer' }}
              >
                ⎋ Sign out
              </button>
            )}
            <div style={{ padding: '10px 10px 2px', ...mono, fontSize: 11, color: 'var(--faint)' }}>
              {noDB ? 'in-memory store' : 'postgresql'}
            </div>
          </div>
        </div>

        {/* content */}
        <div style={{ flex: 1, minWidth: 0, padding: screen === 'playground' ? 0 : '28px 32px 60px', maxWidth: screen === 'playground' ? undefined : 1240 }}>
          {screen === 'overview' && <Overview period={period} setPeriod={setPeriod} />}
          {screen === 'events' && <DlpEvents toast={toast} />}
          {screen === 'policies' && <Policies toast={toast} />}
          {screen === 'report' && <Report toast={toast} />}
          {screen === 'settings' && <Settings noDB={noDB} signedIn={accounts} toast={toast} />}
          {screen === 'playground' && <ChatApp />}
          {screen === 'members' && me && <Members me={me} toast={toast} />}
          {screen === 'tokens' && <Tokens toast={toast} />}
          {screen === 'organization' && me && onMe && (
            <Organization me={me} onMe={onMe} onSignedOut={() => signOut()} toast={toast} />
          )}
        </div>
      </div>

      {/* toasts */}
      <div style={{ position: 'fixed', bottom: 22, right: 22, display: 'flex', flexDirection: 'column', gap: 8, zIndex: 50 }} aria-live="polite">
        {toasts.map((t) => (
          <div key={t.id} style={{ background: 'var(--bg4)', border: '1px solid var(--border2)', color: 'var(--text)', padding: '11px 18px', borderRadius: 9, fontSize: 13.5, boxShadow: 'var(--shadow)', animation: 'ap-toast 0.2s ease-out', display: 'flex', alignItems: 'center', gap: 9 }}>
            <span style={{ width: 7, height: 7, borderRadius: '50%', background: 'var(--green)', display: 'inline-block' }} />
            {t.msg}
          </div>
        ))}
      </div>
    </div>
  )
}

/**
 * The organization this console is showing, and the others the person could
 * show instead. A single organization is a label, not a menu.
 */
function OrgSwitcher({ me, onSwitch }: { me: Me; onSwitch: (orgID: string) => void }) {
  const current = me.organization!
  const others = me.organizations.filter((o) => o.id !== current.id)
  const label = (
    <>
      <div style={{ fontSize: 13, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{current.name}</div>
      <div style={{ fontSize: 11.5, color: 'var(--muted)' }}>{me.role}</div>
    </>
  )
  const box = { background: 'var(--bg3)', border: '1px solid var(--border)', borderRadius: 8, padding: '8px 10px', marginBottom: 14 } as const
  if (others.length === 0) return <div style={box}>{label}</div>
  return (
    <label style={{ ...box, display: 'block', position: 'relative', cursor: 'pointer' }}>
      {label}
      <span aria-hidden="true" style={{ position: 'absolute', right: 10, top: '50%', transform: 'translateY(-50%)', color: 'var(--muted)', fontSize: 11 }}>
        ⇅
      </span>
      {/* A native select laid over the label: keyboard and screen readers get
          a real control, and the sidebar keeps its own look. */}
      <select
        aria-label="Switch organization"
        value={current.id}
        onChange={(e) => onSwitch(e.target.value)}
        style={{ position: 'absolute', inset: 0, opacity: 0, cursor: 'pointer', width: '100%' }}
      >
        {me.organizations.map((o) => (
          <option key={o.id} value={o.id}>
            {o.name} ({o.role})
          </option>
        ))}
      </select>
    </label>
  )
}
