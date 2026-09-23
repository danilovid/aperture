import { useCallback, useEffect, useRef, useState } from 'react'
import './theme.css'
import { api, ApiError, auth, atLeast } from '../api'
import type { Me, Period, Role } from '../api'
import { navigate, Link } from '../router'
import type { Theme } from '../theme'
import { Logo } from './ui'
import { Icon } from './icons'
import type { IconName } from './icons'
import { mono } from './styles'
import { Overview } from './Overview'
import { DlpEvents } from './DlpEvents'
import { Policies } from './Policies'
import { Report } from './Report'
import { Settings } from './Settings'
import { Members } from './Members'
import { Tokens } from './Tokens'
import { Audit } from './Audit'
import { Organization } from './Organization'
import { Account } from './Account'
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
  | 'audit'
  | 'organization'
  | 'account'

interface NavItem {
  id: Screen
  label: string
  icon: IconName
  /** Hidden below this role in a signed-in console. */
  min: Role
  /** Only exists where there are accounts. */
  accountsOnly?: boolean
}

// The server decides what a role may do; the sidebar only avoids showing a
// screen whose every request would be refused.
const traffic: NavItem[] = [
  { id: 'overview', label: 'Overview', icon: 'overview', min: 'viewer' },
  { id: 'events', label: 'DLP Events', icon: 'events', min: 'viewer' },
  { id: 'policies', label: 'Policies', icon: 'policies', min: 'viewer' },
  { id: 'report', label: 'Report', icon: 'report', min: 'viewer' },
  { id: 'settings', label: 'Settings & Keys', icon: 'settings', min: 'admin' },
  { id: 'playground', label: 'Playground', icon: 'playground', min: 'member' },
]
const organization: NavItem[] = [
  { id: 'members', label: 'Members', icon: 'members', min: 'viewer', accountsOnly: true },
  { id: 'tokens', label: 'Access tokens', icon: 'tokens', min: 'admin', accountsOnly: true },
  { id: 'audit', label: 'Audit log', icon: 'audit', min: 'admin', accountsOnly: true },
  { id: 'organization', label: 'Organization', icon: 'organization', min: 'viewer', accountsOnly: true },
]

interface Toast {
  id: number
  msg: string
}

/**
 * The screen a path names: /app/<screen>, anything else is the overview. The
 * account page is not in the sidebar's lists — it is reached from your own
 * name — but it is a screen every signed-in person has.
 */
function screenOf(path: string, allowed: NavItem[], accounts: boolean): Screen {
  const id = path.split('/')[2] as Screen | undefined
  if (id === 'account' && accounts) return 'account'
  return allowed.some((n) => n.id === id) ? (id as Screen) : 'overview'
}

export function Console({
  theme,
  toggleTheme,
  path,
  query,
  me,
  onMe,
  onSignOut,
}: {
  theme: Theme
  toggleTheme: () => void
  path: string
  query: URLSearchParams
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
  const screen = screenOf(path, [...trafficNav, ...orgNav], accounts)

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

  const navButton = (n: NavItem) => {
    const active = screen === n.id
    return (
      <Link
        key={n.id}
        to={`/app/${n.id}`}
        className="ap-nav-btn"
        aria-current={active ? 'page' : undefined}
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 10,
          background: active ? 'var(--bg4)' : 'none',
          color: active ? 'var(--text)' : 'var(--muted)',
          padding: '7px 10px',
          borderRadius: 8,
          fontSize: 13.5,
          fontWeight: active ? 600 : 500,
        }}
      >
        <Icon name={n.icon} />
        <span style={{ flex: 1 }}>{n.label}</span>
        {n.id === 'events' && blockedBadge > 0 && (
          <span style={{ ...mono, fontSize: 11, background: 'var(--red-bg)', color: 'var(--red)', padding: '1px 7px', borderRadius: 99 }}>
            {blockedBadge}
          </span>
        )}
      </Link>
    )
  }

  return (
    <div className="ap-root" data-ap-theme={theme}>
      <div style={{ display: 'flex', minHeight: '100vh' }}>
        {/* sidebar */}
        <div style={{ width: 240, flexShrink: 0, background: 'var(--bg3)', borderRight: '1px solid var(--border)', display: 'flex', flexDirection: 'column', padding: '16px 12px 12px', position: 'sticky', top: 0, height: '100vh', boxSizing: 'border-box', overflowY: 'auto' }}>
          <Link
            to="/app/overview"
            aria-label="Aperture — overview"
            className="ap-nav-btn"
            style={{ display: 'flex', alignItems: 'center', gap: 9, padding: '6px 10px', margin: '0 0 14px', borderRadius: 8, color: 'var(--text)' }}
          >
            <Logo />
            <span className="ap-serif" style={{ fontSize: 18 }}>Aperture</span>
          </Link>

          <nav aria-label="Traffic" style={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
            {trafficNav.map(navButton)}
          </nav>

          {orgNav.length > 0 && (
            <nav aria-label="Organization" style={{ display: 'flex', flexDirection: 'column', gap: 1, marginTop: 20 }}>
              <div style={{ fontSize: 11.5, color: 'var(--faint)', fontWeight: 600, padding: '0 10px 6px' }}>Organization</div>
              {orgNav.map(navButton)}
            </nav>
          )}

          <div style={{ flex: 1 }} />
          {me?.organization ? (
            <AccountMenu
              me={me}
              theme={theme}
              toggleTheme={toggleTheme}
              storage={noDB ? 'in-memory store' : 'postgresql'}
              current={screen === 'account'}
              onSwitch={switchOrg}
              onSignOut={() => signOut()}
            />
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 2, borderTop: '1px solid var(--border)', paddingTop: 10 }}>
              <button
                onClick={toggleTheme}
                className="ap-nav-btn"
                style={{ display: 'flex', alignItems: 'center', gap: 10, background: 'none', border: 'none', textAlign: 'left', padding: '7px 10px', borderRadius: 8, fontSize: 13.5, color: 'var(--muted)', cursor: 'pointer' }}
              >
                <Icon name={theme === 'dark' ? 'sun' : 'moon'} />
                {theme === 'dark' ? 'Light theme' : 'Dark theme'}
              </button>
              <div style={{ padding: '6px 10px 2px', ...mono, fontSize: 11, color: 'var(--faint)' }}>
                {noDB ? 'in-memory store' : 'postgresql'}
              </div>
            </div>
          )}
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
          {screen === 'audit' && <Audit />}
          {screen === 'organization' && me && onMe && (
            <Organization me={me} onMe={onMe} onSignedOut={() => signOut()} toast={toast} />
          )}
          {screen === 'account' && me && <Account me={me} query={query} onSignOut={signOut} toast={toast} />}
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
 * Who is signed in and where, at the foot of the sidebar: the person, the
 * organization they are looking at, and a menu to switch organization, open
 * their account, change the theme or sign out.
 */
function AccountMenu({
  me,
  theme,
  toggleTheme,
  storage,
  current,
  onSwitch,
  onSignOut,
}: {
  me: Me
  theme: Theme
  toggleTheme: () => void
  storage: string
  current: boolean
  onSwitch: (orgID: string) => void
  onSignOut: () => void
}) {
  const [open, setOpen] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const org = me.organization!

  useEffect(() => {
    if (!open) return
    const outside = (e: MouseEvent) => {
      if (!box.current?.contains(e.target as Node)) setOpen(false)
    }
    const escape = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', outside)
    document.addEventListener('keydown', escape)
    return () => {
      document.removeEventListener('mousedown', outside)
      document.removeEventListener('keydown', escape)
    }
  }, [open])

  const who = me.user.name || me.user.email
  const initials =
    (me.user.name || me.user.email.split('@')[0])
      .split(/[\s._-]+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((w) => w[0]?.toUpperCase())
      .join('') || '?'

  const item: React.CSSProperties = {
    display: 'flex',
    alignItems: 'center',
    gap: 10,
    width: '100%',
    background: 'none',
    border: 'none',
    textAlign: 'left',
    padding: '7px 10px',
    borderRadius: 7,
    fontSize: 13.5,
    color: 'var(--text)',
    cursor: 'pointer',
  }

  return (
    <div ref={box} style={{ position: 'relative', borderTop: '1px solid var(--border)', paddingTop: 10 }}>
      {open && (
        <div
          role="menu"
          aria-label="Account"
          style={{ position: 'absolute', bottom: 'calc(100% + 6px)', left: 0, right: 0, background: 'var(--bg2)', border: '1px solid var(--border2)', borderRadius: 12, boxShadow: 'var(--shadow)', padding: 6, zIndex: 30 }}
        >
          <div style={{ padding: '6px 10px 8px', fontSize: 12.5, color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {me.user.email}
          </div>
          <div style={{ fontSize: 11.5, color: 'var(--faint)', fontWeight: 600, padding: '4px 10px' }}>Organizations</div>
          {me.organizations.map((o) => (
            <button
              key={o.id}
              role="menuitemradio"
              aria-checked={o.id === org.id}
              className="ap-nav-btn"
              onClick={() => {
                setOpen(false)
                if (o.id !== org.id) onSwitch(o.id)
              }}
              style={item}
            >
              <span style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{o.name}</span>
              <span style={{ fontSize: 12, color: 'var(--faint)' }}>{o.role}</span>
              <span style={{ width: 16, color: 'var(--accent)' }}>{o.id === org.id && <Icon name="check" />}</span>
            </button>
          ))}
          <div style={{ height: 1, background: 'var(--border)', margin: '6px 4px' }} />
          <Link to="/app/account" role="menuitem" className="ap-nav-btn" onClick={() => setOpen(false)} style={item}>
            <Icon name="account" />
            Account
          </Link>
          <button role="menuitem" className="ap-nav-btn" onClick={toggleTheme} style={item}>
            <Icon name={theme === 'dark' ? 'sun' : 'moon'} />
            {theme === 'dark' ? 'Light theme' : 'Dark theme'}
          </button>
          <button role="menuitem" className="ap-nav-btn" onClick={onSignOut} style={item}>
            <Icon name="signout" />
            Sign out
          </button>
          <div style={{ padding: '6px 10px 2px', ...mono, fontSize: 11, color: 'var(--faint)' }}>{storage}</div>
        </div>
      )}
      <button
        aria-haspopup="menu"
        aria-expanded={open}
        title={`${me.user.email} · ${org.name}`}
        className="ap-nav-btn"
        onClick={() => setOpen((v) => !v)}
        style={{ display: 'flex', alignItems: 'center', gap: 10, width: '100%', background: open || current ? 'var(--bg4)' : 'none', border: 'none', textAlign: 'left', padding: '7px 8px', borderRadius: 8, cursor: 'pointer', color: 'var(--text)' }}
      >
        <span aria-hidden="true" style={{ width: 30, height: 30, borderRadius: '50%', background: 'var(--accent-dim)', color: 'var(--accent)', display: 'inline-flex', alignItems: 'center', justifyContent: 'center', fontSize: 12, fontWeight: 700, flexShrink: 0 }}>
          {initials}
        </span>
        <span style={{ flex: 1, minWidth: 0 }}>
          <span style={{ display: 'block', fontSize: 13, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{who}</span>
          <span style={{ display: 'block', fontSize: 12, color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {org.name} · {me.role}
          </span>
        </span>
        <span style={{ color: 'var(--faint)', transform: open ? 'rotate(180deg)' : undefined, display: 'inline-flex' }}>
          <Icon name="chevron" />
        </span>
      </button>
    </div>
  )
}
