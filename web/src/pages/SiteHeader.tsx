// The bar across the public pages: the mark, the guides, and the way in.
import { Link } from '../router'
import { useSignInOptions } from './oauth'
import { Logo } from '../console/ui'
import type { Theme } from '../theme'

const ghost = { padding: '7px 11px', borderRadius: 7, fontSize: 13.5, color: 'var(--muted)' } as const
const accent = { background: 'var(--accent)', color: 'var(--on-accent)', padding: '8px 16px', borderRadius: 7, fontSize: 13.5, fontWeight: 600 } as const

export function SiteHeader({ theme, toggleTheme, signedIn = false }: { theme: Theme; toggleTheme: () => void; signedIn?: boolean }) {
  const { registration } = useSignInOptions()
  return (
    <header className="ap-landing-bar">
      <Link to="/" style={{ display: 'flex', alignItems: 'center', gap: 9, color: 'var(--text)' }}>
        <Logo />
        <span className="ap-display ap-brand-text" style={{ fontSize: 18 }}>Mutegate</span>
      </Link>
      <nav style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <Link to="/connect" className="ap-ghost-btn" style={ghost}>
          Connect
        </Link>
        <a href="https://github.com/Mutegate/mutegate" className="ap-ghost-btn ap-wide-only" style={ghost}>
          GitHub
        </a>
        <button onClick={toggleTheme} className="ap-ghost-btn" aria-label="Switch theme" style={{ background: 'none', border: 'none', padding: '7px 10px', borderRadius: 7, fontSize: 14, color: 'var(--muted)', cursor: 'pointer' }}>
          {theme === 'dark' ? '☀' : '☾'}
        </button>
        {signedIn ? (
          <Link to="/app" className="ap-accent-btn" style={accent}>
            Open the console
          </Link>
        ) : registration ? (
          <>
            <Link to="/login" className="ap-ghost-btn" style={{ ...ghost, color: 'var(--text)' }}>
              Sign in
            </Link>
            <Link to="/signup" className="ap-accent-btn" style={accent}>
              Sign up
            </Link>
          </>
        ) : (
          <Link to="/login" className="ap-accent-btn" style={accent}>
            Sign in
          </Link>
        )}
      </nav>
    </header>
  )
}
