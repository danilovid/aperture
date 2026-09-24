// Who is looking, and therefore what they see.
//
// One question on load — GET /api/auth/me — decides everything:
//   200  signed in: the console, in the organization on their session;
//   401  a visitor: the landing page, sign-in, invitations;
//   503  an installation without a database, so without accounts: the console
//        as it always was, authenticated with the admin key from Settings.
// The third keeps a `docker run` quickstart working exactly as before; accounts
// only exist where there is somewhere to keep them.
import { useCallback, useEffect, useState } from 'react'
import { auth, ApiError, setAuthMode, setUnauthorizedHandler } from './api'
import type { Me } from './api'
import { match, navigate, useLocation } from './router'
import { useTheme } from './theme'
import { Landing } from './pages/Landing'
import { Invite, Login, Signup } from './pages/Auth'
import { Connect } from './pages/Connect'
import { Console } from './console/Console'
import { Logo } from './console/ui'
import { Button } from './console/forms'
import './console/theme.css'

type Boot =
  | { state: 'loading' }
  | { state: 'visitor' }
  | { state: 'signed-in'; me: Me }
  | { state: 'no-accounts' }
  | { state: 'unreachable'; message: string }

/** A redirect as a component, so it happens after render rather than during. */
function Redirect({ to }: { to: string }) {
  useEffect(() => navigate(to, { replace: true }), [to])
  return null
}

export function Root() {
  const [theme, toggleTheme] = useTheme()
  const { path, query } = useLocation()
  const [boot, setBoot] = useState<Boot>({ state: 'loading' })

  // State is only set once the answer arrives, never synchronously: the
  // first render is already "loading", and a retry says so itself.
  const probe = useCallback(() => {
    auth
      .me()
      .then((me) => {
        setAuthMode('session')
        setBoot({ state: 'signed-in', me })
      })
      .catch((e) => {
        if (e instanceof ApiError && e.status === 401) {
          setAuthMode('session')
          setBoot({ state: 'visitor' })
        } else if (e instanceof ApiError && e.status === 503) {
          setAuthMode('legacy')
          setBoot({ state: 'no-accounts' })
        } else {
          setBoot({ state: 'unreachable', message: e instanceof Error ? e.message : String(e) })
        }
      })
  }, [])

  useEffect(() => probe(), [probe])

  // A session can end while the console is open: it expired, somebody signed
  // out everywhere, or an admin removed them. A 401 from any endpoint is only
  // a hint, though — some answer 401 to a session simply because they are not
  // for sessions (the operator's alert settings). So ask the one endpoint whose
  // 401 means exactly "not signed in", and only then send them to sign in,
  // with a way back to the page they were on.
  useEffect(() => {
    let checking = false
    setUnauthorizedHandler(() => {
      if (checking) return
      checking = true
      auth
        .me()
        .then((me) => setBoot({ state: 'signed-in', me }))
        .catch((e) => {
          if (e instanceof ApiError && e.status === 401) {
            setBoot({ state: 'visitor' })
            const here = window.location.pathname + window.location.search
            navigate(`/login?next=${encodeURIComponent(here)}`, { replace: true })
          }
        })
        .finally(() => {
          checking = false
        })
    })
    return () => setUnauthorizedHandler(null)
  }, [])

  const signedIn = useCallback((me: Me) => {
    setAuthMode('session')
    setBoot({ state: 'signed-in', me })
  }, [])

  const signOut = useCallback(async (everywhere = false) => {
    try {
      await auth.logout(everywhere)
    } catch {
      // The cookie is cleared by the server on the way out whether or not
      // the session still existed; nothing useful to do with an error here.
    }
    setBoot({ state: 'visitor' })
  }, [])

  if (boot.state === 'loading') {
    return <div className="ap-root" data-ap-theme={theme} />
  }

  if (boot.state === 'unreachable') {
    return (
      <div className="ap-root" data-ap-theme={theme}>
        <div className="ap-auth">
          <Logo size={40} color="var(--faint)" />
          <div style={{ fontWeight: 600, fontSize: 16, margin: '16px 0 6px' }}>The gateway is not answering</div>
          <div style={{ color: 'var(--muted)', fontSize: 13.5, marginBottom: 18, maxWidth: 420, textAlign: 'center' }}>
            {boot.message}
          </div>
          <Button
            onClick={() => {
              setBoot({ state: 'loading' })
              probe()
            }}
          >
            Try again
          </Button>
        </div>
      </div>
    )
  }

  // The guides are for anybody, signed in or not, with accounts or without.
  const connect = path === '/connect' ? {} : match('/connect/:tool', path)
  if (connect) {
    return <Connect theme={theme} toggleTheme={toggleTheme} tool={connect.tool} signedIn={boot.state === 'signed-in'} />
  }

  if (boot.state === 'no-accounts') {
    return <Console theme={theme} toggleTheme={toggleTheme} path={path} query={query} />
  }

  const invite = match('/invite/:token', path)

  if (boot.state === 'visitor') {
    if (path === '/') return <Landing theme={theme} toggleTheme={toggleTheme} />
    if (path === '/login') return <Login theme={theme} query={query} onSignedIn={signedIn} />
    if (path === '/signup') return <Signup theme={theme} onSignedIn={signedIn} />
    if (invite) {
      return <Invite theme={theme} token={invite.token} query={query} me={null} onSignedIn={signedIn} onSignOut={() => signOut()} />
    }
    if (path.startsWith('/app')) return <Redirect to={`/login?next=${encodeURIComponent(path)}`} />
    return <Redirect to="/" />
  }

  // Signed in.
  const me = boot.me
  if (invite) {
    return <Invite theme={theme} token={invite.token} query={query} me={me} onSignedIn={signedIn} onSignOut={() => signOut()} />
  }
  if (!path.startsWith('/app')) return <Redirect to="/app" />
  if (!me.organization) {
    return <NoOrganization theme={theme} me={me} onMe={signedIn} onSignOut={() => signOut()} />
  }
  return (
    // Keyed by organization: switching remounts the console, so nothing
    // fetched for one organization is ever shown under another's name.
    <Console
      key={me.organization.id}
      theme={theme}
      toggleTheme={toggleTheme}
      path={path}
      query={query}
      me={me}
      onMe={signedIn}
      onSignOut={signOut}
    />
  )
}

/**
 * Signed in, with no organization on the session: the one they were in was
 * closed or they left it. If they belong anywhere else, offer it; otherwise
 * say what would get them back in.
 */
function NoOrganization({
  theme,
  me,
  onMe,
  onSignOut,
}: {
  theme: 'dark' | 'light'
  me: Me
  onMe: (me: Me) => void
  onSignOut: () => void
}) {
  const [error, setError] = useState('')
  const open = async (orgID: string) => {
    try {
      onMe(await auth.switchOrg(orgID))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }
  const others = me.organizations
  return (
    <div className="ap-root" data-ap-theme={theme}>
      <div className="ap-auth">
        <div className="ap-auth-card" style={{ textAlign: 'center' }}>
          <Logo size={40} color="var(--faint)" />
          <div style={{ fontWeight: 600, fontSize: 16, margin: '16px 0 6px' }}>
            {others.length > 0 ? 'Choose an organization' : "You're not in any organization"}
          </div>
          <div style={{ color: 'var(--muted)', fontSize: 13.5, marginBottom: 20 }}>
            {others.length > 0
              ? `Signed in as ${me.user.email}.`
              : `Signed in as ${me.user.email}. Ask an admin to invite you, then open the link while signed in.`}
          </div>
          {others.length > 0 && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 16, textAlign: 'left' }}>
              {others.map((o) => (
                <Button key={o.id} onClick={() => open(o.id)} style={{ display: 'flex', justifyContent: 'space-between' }}>
                  <span>{o.name}</span>
                  <span style={{ color: 'var(--muted)', fontWeight: 500 }}>{o.role}</span>
                </Button>
              ))}
            </div>
          )}
          {error && <div style={{ color: 'var(--red)', fontSize: 13, marginBottom: 12 }}>{error}</div>}
          <Button onClick={onSignOut} style={{ width: others.length > 0 ? '100%' : undefined }}>
            Sign out
          </Button>
        </div>
      </div>
    </div>
  )
}
