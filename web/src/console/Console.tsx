import { useCallback, useEffect, useState } from 'react'
import './theme.css'
import { api, ApiError } from '../api'
import type { Period } from '../api'
import { Logo } from './ui'
import { mono } from './styles'
import { Overview } from './Overview'
import { DlpEvents } from './DlpEvents'
import { Policies } from './Policies'
import { Settings } from './Settings'
import ChatApp from '../App'

type Screen = 'overview' | 'events' | 'policies' | 'settings' | 'playground'

interface Toast {
  id: number
  msg: string
}

const THEME_STORAGE = 'aperture-theme'

export function Console() {
  const [screen, setScreen] = useState<Screen>('overview')
  const [theme, setTheme] = useState<'dark' | 'light'>(() => {
    const saved = localStorage.getItem(THEME_STORAGE)
    if (saved === 'dark' || saved === 'light') return saved
    return window.matchMedia?.('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
  })
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

  const nav: { id: Screen; label: string; badge?: number }[] = [
    { id: 'overview', label: 'Overview' },
    { id: 'events', label: 'DLP Events', badge: blockedBadge || undefined },
    { id: 'policies', label: 'Policies' },
    { id: 'settings', label: 'Settings & Keys' },
    { id: 'playground', label: 'Playground' },
  ]

  return (
    <div className="ap-root" data-ap-theme={theme}>
      <div style={{ display: 'flex', minHeight: '100vh' }}>
        {/* sidebar */}
        <div style={{ width: 216, flexShrink: 0, background: 'var(--bg2)', borderRight: '1px solid var(--border)', display: 'flex', flexDirection: 'column', padding: '18px 12px', position: 'sticky', top: 0, height: '100vh' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 9, padding: '4px 10px 18px' }}>
            <Logo />
            <span style={{ fontWeight: 700, fontSize: 15.5 }}>Aperture</span>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            {nav.map((n) => (
              <button
                key={n.id}
                onClick={() => setScreen(n.id)}
                className="ap-nav-btn"
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  gap: 8,
                  background: screen === n.id ? 'var(--bg3)' : 'none',
                  color: screen === n.id ? 'var(--accent)' : 'var(--text)',
                  border: 'none',
                  textAlign: 'left',
                  padding: '8px 10px',
                  borderRadius: 7,
                  fontSize: 13.5,
                  fontWeight: 500,
                  cursor: 'pointer',
                  width: '100%',
                }}
              >
                <span>{n.label}</span>
                {n.badge !== undefined && (
                  <span style={{ ...mono, fontSize: 11, background: 'var(--red-bg)', color: 'var(--red)', padding: '1px 7px', borderRadius: 99 }}>
                    {n.badge}
                  </span>
                )}
              </button>
            ))}
          </div>
          <div style={{ flex: 1 }} />
          <div style={{ display: 'flex', flexDirection: 'column', gap: 2, borderTop: '1px solid var(--border)', paddingTop: 12 }}>
            <button
              onClick={() => {
                const next = theme === 'dark' ? 'light' : 'dark'
                setTheme(next)
                localStorage.setItem(THEME_STORAGE, next)
              }}
              className="ap-ghost-btn"
              style={{ background: 'none', border: 'none', textAlign: 'left', padding: '8px 10px', borderRadius: 7, fontSize: 13, color: 'var(--muted)', cursor: 'pointer' }}
            >
              {theme === 'dark' ? '☀ Light theme' : '☾ Dark theme'}
            </button>
            <div style={{ padding: '10px 10px 2px', ...mono, fontSize: 11, color: 'var(--faint)' }}>
              {noDB ? 'in-memory store' : 'postgresql'}
            </div>
          </div>
        </div>

        {/* content */}
        <div style={{ flex: 1, minWidth: 0, padding: screen === 'playground' ? 0 : '28px 32px 60px', maxWidth: screen === 'playground' ? undefined : 1240 }}>
          {screen === 'overview' && <Overview period={period} setPeriod={setPeriod} />}
          {screen === 'events' && <DlpEvents />}
          {screen === 'policies' && <Policies toast={toast} />}
          {screen === 'settings' && <Settings noDB={noDB} toast={toast} />}
          {screen === 'playground' && <ChatApp />}
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
