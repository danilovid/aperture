// Signing in, and arriving through an invitation.
//
// Registration is closed: the only way to create an account is the link an
// admin sends, which is why there is no /register page of its own. The
// invitation page is the registration page, and it knows who it is for.
import { useEffect, useState } from 'react'
import type { FormEvent, ReactNode } from 'react'
import { auth, people, ApiError } from '../api'
import type { InvitationPreview, Me } from '../api'
import { Link, navigate } from '../router'
import { Logo } from '../console/ui'
import { mono } from '../console/styles'
import { Button, Field, Notice, TextInput } from '../console/forms'
import type { Theme } from '../theme'
import { ProviderButtons } from './ProviderButtons'
import { oauthErrorText, useProviders } from './oauth'

const MIN_PASSWORD = 10 // auth.MinPasswordLen on the server

function AuthFrame({ theme, title, sub, children, foot }: { theme: Theme; title: string; sub?: ReactNode; children: ReactNode; foot?: ReactNode }) {
  return (
    <div className="ap-root" data-ap-theme={theme}>
      <div className="ap-auth">
        <Link to="/" style={{ display: 'flex', alignItems: 'center', gap: 9, marginBottom: 22, color: 'var(--text)' }}>
          <Logo />
          <span style={{ fontWeight: 700, fontSize: 15.5 }}>Aperture</span>
        </Link>
        <div className="ap-auth-card">
          <h1 style={{ fontSize: 20, fontWeight: 700, margin: 0, letterSpacing: '-0.2px' }}>{title}</h1>
          {sub && <div style={{ color: 'var(--muted)', fontSize: 13.5, marginTop: 6 }}>{sub}</div>}
          <div style={{ marginTop: 22 }}>{children}</div>
        </div>
        {foot && <div style={{ marginTop: 18, fontSize: 13, color: 'var(--muted)', textAlign: 'center', maxWidth: 400 }}>{foot}</div>}
      </div>
    </div>
  )
}

/** Where to go after signing in: back where they were, but never off-site. */
function safeNext(next: string | null): string {
  return next && next.startsWith('/app') ? next : '/app'
}

export function Login({ theme, query, onSignedIn }: { theme: Theme; query: URLSearchParams; onSignedIn: (me: Me) => void }) {
  const next = query.get('next')
  const providers = useProviders()
  const providerError = oauthErrorText(query.get('oauth_error'), query.get('provider'))
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const me = await auth.login(email, password)
      onSignedIn(me)
      navigate(safeNext(next), { replace: true })
    } catch (err) {
      // The server's answer is deliberately the same for a wrong address and
      // a wrong password; the page repeats it rather than guessing further.
      setError(err instanceof ApiError ? err.message : 'Could not reach the gateway.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <AuthFrame
      theme={theme}
      title="Sign in"
      sub="to your organization's console"
      foot={<>No account? Accounts are by invitation — ask an admin of your organization for a link.</>}
    >
      <form onSubmit={submit} style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        {providerError && <Notice>{providerError}</Notice>}
        <ProviderButtons providers={providers} opts={{ next: safeNext(next) }} />
        <Field label="Email">
          <TextInput type="email" autoComplete="username" required autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
        </Field>
        <Field label="Password">
          <TextInput type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
        </Field>
        {error && <Notice>{error}</Notice>}
        <Button tone="accent" type="submit" busy={busy} style={{ marginTop: 4 }}>
          Sign in
        </Button>
      </form>
    </AuthFrame>
  )
}

const roleWords: Record<string, string> = {
  owner: 'an owner',
  admin: 'an admin',
  member: 'a member',
  viewer: 'a viewer',
}

/**
 * /invite/:token. What it shows depends on who opens it: a stranger gets a
 * form to create the account the invitation is for, somebody with an account
 * signs in first, and somebody already signed in just joins.
 */
export function Invite({
  theme,
  token,
  query,
  me,
  onSignedIn,
  onSignOut,
}: {
  theme: Theme
  token: string
  query: URLSearchParams
  me: Me | null
  onSignedIn: (me: Me) => void
  onSignOut: () => Promise<void>
}) {
  const providers = useProviders()
  const providerError = oauthErrorText(query.get('oauth_error'), query.get('provider'))
  const [preview, setPreview] = useState<InvitationPreview | null>(null)
  const [invalid, setInvalid] = useState(false)
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let live = true
    people
      .lookupInvitation(token)
      .then((p) => live && setPreview(p))
      .catch(() => live && setInvalid(true))
    return () => {
      live = false
    }
  }, [token])

  const run = async (action: () => Promise<Me>) => {
    setBusy(true)
    setError('')
    try {
      const joined = await action()
      onSignedIn(joined)
      navigate('/app', { replace: true })
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not reach the gateway.')
    } finally {
      setBusy(false)
    }
  }

  if (invalid) {
    return (
      <AuthFrame theme={theme} title="This invitation is no longer valid" foot={<Link to="/login">Sign in instead</Link>}>
        <div style={{ color: 'var(--muted)', fontSize: 13.5 }}>
          It may have been used already, revoked, or it expired — invitations last seven days. Ask whoever sent it for a
          new one.
        </div>
      </AuthFrame>
    )
  }
  if (!preview) {
    return (
      <AuthFrame theme={theme} title="Opening your invitation…">
        <div style={{ height: 40 }} />
      </AuthFrame>
    )
  }

  const org = preview.organization.name
  const sub = (
    <>
      Join <strong style={{ color: 'var(--text)' }}>{org}</strong> as {roleWords[preview.role] ?? preview.role}.
    </>
  )
  const forThem = me && me.user.email.toLowerCase() === preview.email.toLowerCase()

  // Signed in as the right person: one click.
  if (me && forThem) {
    return (
      <AuthFrame theme={theme} title={`You're invited to ${org}`} sub={sub}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div style={{ fontSize: 13.5, color: 'var(--muted)' }}>
            Signed in as <span style={{ ...mono, color: 'var(--text)' }}>{me.user.email}</span>. {org} will appear in your
            organization switcher.
          </div>
          {error && <Notice>{error}</Notice>}
          <Button tone="accent" busy={busy} onClick={() => run(() => people.acceptInvitation(token))}>
            Join {org}
          </Button>
        </div>
      </AuthFrame>
    )
  }

  // Signed in as somebody else: the invitation is bound to its address, so
  // say so plainly rather than failing on the button.
  if (me && !forThem) {
    return (
      <AuthFrame theme={theme} title="This invitation is for someone else" sub={sub}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div style={{ fontSize: 13.5, color: 'var(--muted)' }}>
            It was sent to <span style={{ ...mono, color: 'var(--text)' }}>{preview.email}</span>, and you're signed in as{' '}
            <span style={{ ...mono, color: 'var(--text)' }}>{me.user.email}</span>.
          </div>
          <Button tone="plain" onClick={() => void onSignOut()}>
            Sign out and continue as {preview.email}
          </Button>
        </div>
      </AuthFrame>
    )
  }

  // An account exists for the address: sign in, then join in the same step.
  if (preview.account_exists) {
    return (
      <AuthFrame theme={theme} title={`Sign in to join ${org}`} sub={sub}>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void run(async () => {
              await auth.login(preview.email, password)
              return people.acceptInvitation(token)
            })
          }}
          style={{ display: 'flex', flexDirection: 'column', gap: 14 }}
        >
          {providerError && <Notice>{providerError}</Notice>}
          <ProviderButtons providers={providers} opts={{ invite: token }} />
          <Field label="Email">
            <TextInput type="email" autoComplete="username" readOnly value={preview.email} />
          </Field>
          <Field label="Password">
            <TextInput type="password" autoComplete="current-password" required autoFocus value={password} onChange={(e) => setPassword(e.target.value)} />
          </Field>
          {error && <Notice>{error}</Notice>}
          <Button tone="accent" type="submit" busy={busy}>
            Sign in and join
          </Button>
        </form>
      </AuthFrame>
    )
  }

  // A new person: this is registration.
  const tooShort = password.length > 0 && password.length < MIN_PASSWORD
  return (
    <AuthFrame theme={theme} title="Create your account" sub={sub} foot={<>Already have an account? <Link to={`/login`}>Sign in</Link></>}>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          void run(() => auth.register(token, preview.email, name, password))
        }}
        style={{ display: 'flex', flexDirection: 'column', gap: 14 }}
      >
        {providerError && <Notice>{providerError}</Notice>}
        <ProviderButtons providers={providers} opts={{ invite: token }} label="Join with" />
        <Field label="Email" hint="The invitation was sent to this address, and it is the one you will sign in with.">
          <TextInput type="email" autoComplete="username" readOnly value={preview.email} />
        </Field>
        <Field label="Your name">
          <TextInput autoComplete="name" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="How colleagues will see you" />
        </Field>
        <Field label="Password" hint={tooShort ? undefined : `At least ${MIN_PASSWORD} characters.`}>
          <TextInput type="password" autoComplete="new-password" required minLength={MIN_PASSWORD} value={password} onChange={(e) => setPassword(e.target.value)} />
        </Field>
        {tooShort && <Notice tone="warn">{MIN_PASSWORD - password.length} more characters to go.</Notice>}
        {error && <Notice>{error}</Notice>}
        <Button tone="accent" type="submit" busy={busy} disabled={password.length < MIN_PASSWORD}>
          Create account and join
        </Button>
      </form>
    </AuthFrame>
  )
}
