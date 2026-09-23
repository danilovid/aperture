// The signed-in person's own things: who they are, the ways they can sign in,
// and signing out of every browser at once. Not the organization's — that is
// the Organization screen.
import { useCallback, useEffect, useRef, useState } from 'react'
import { auth } from '../api'
import { navigate } from '../router'
import type { Identity, Me } from '../api'
import { card, colHead, h1Style, mono, subStyle } from './styles'
import { Button, Notice } from './forms'
import { fmtTs } from './format'
import { ProviderButtons } from '../pages/ProviderButtons'
import { oauthErrorText, providerName, useProviders } from '../pages/oauth'

export function Account({
  me,
  query,
  onSignOut,
  toast,
}: {
  me: Me
  query: URLSearchParams
  onSignOut: (everywhere?: boolean) => Promise<void>
  toast: (msg: string) => void
}) {
  const providers = useProviders()
  const [identities, setIdentities] = useState<Identity[] | null>(null)
  const [hasPassword, setHasPassword] = useState(true)
  const [error, setError] = useState('')
  const [confirmEverywhere, setConfirmEverywhere] = useState(false)
  const providerError = oauthErrorText(query.get('oauth_error'), query.get('provider'))
  const connected = query.get('connected')

  const load = useCallback(async () => {
    try {
      const res = await auth.identities()
      setIdentities(res.identities)
      setHasPassword(res.has_password)
    } catch (e) {
      setError((e as Error).message)
      setIdentities([])
    }
  }, [])

  useEffect(() => {
    // load() only sets state after its awaited call resolves.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load()
  }, [load])

  // Back from connecting a provider: say so once, then drop it from the
  // address so a reload does not say it again. "Once" is held in a ref, not
  // left to the effect running once — React is free to run it twice.
  const announced = useRef<string | null>(null)
  useEffect(() => {
    if (!connected || announced.current === connected) return
    announced.current = connected
    toast(`${providerName(connected)} connected`)
    navigate('/app/account', { replace: true })
  }, [connected, toast])

  const disconnect = async (i: Identity) => {
    try {
      await auth.unlinkIdentity(i.id)
      toast(`${providerName(i.provider)} disconnected`)
      void load()
    } catch (e) {
      toast((e as Error).message)
    }
  }

  const list = identities ?? []
  // An account nobody can sign in to is an account lost; the server refuses
  // it, and the button says why before anybody presses it.
  const onlyWayIn = !hasPassword && list.length <= 1
  const notYet = providers.filter((p) => !list.some((i) => i.provider === p.id))

  return (
    <div style={{ maxWidth: 720 }}>
      <div style={{ marginBottom: 20 }}>
        <h1 style={h1Style}>Your account</h1>
        <div style={subStyle}>Yours alone, in every organization you belong to</div>
      </div>

      <div style={{ ...card, padding: '16px 20px', marginBottom: 28, display: 'grid', gridTemplateColumns: '120px 1fr', gap: '10px 16px', fontSize: 13.5 }}>
        <span style={{ color: 'var(--muted)' }}>Name</span>
        <span>{me.user.name || '—'}</span>
        <span style={{ color: 'var(--muted)' }}>Email</span>
        <span style={mono}>{me.user.email}</span>
        <span style={{ color: 'var(--muted)' }}>Member of</span>
        <span>{me.organizations.map((o) => o.name).join(', ') || '—'}</span>
      </div>

      <div style={{ ...colHead, marginBottom: 10 }}>Ways to sign in</div>
      {providerError && (
        <div style={{ marginBottom: 12 }}>
          <Notice>{providerError}</Notice>
        </div>
      )}
      <div style={{ ...card, overflow: 'hidden', marginBottom: 12 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '13px 18px', borderBottom: '1px solid var(--border)' }}>
          <span style={{ fontSize: 13.5, fontWeight: 500, flex: 1 }}>Password</span>
          <span style={{ fontSize: 12.5, color: hasPassword ? 'var(--green)' : 'var(--faint)' }}>
            {hasPassword ? 'set' : 'none — you sign in through a provider'}
          </span>
        </div>
        {list.map((i) => (
          <div key={i.id} style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '13px 18px', borderBottom: '1px solid var(--border)' }}>
            <span style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontSize: 13.5, fontWeight: 500 }}>{providerName(i.provider)}</div>
              <div style={{ ...mono, fontSize: 12, color: 'var(--muted)' }}>
                {i.email || 'no address shared'} · connected {fmtTs(i.created_at).split(',')[0]}
              </div>
            </span>
            {onlyWayIn ? (
              <span style={{ fontSize: 12, color: 'var(--faint)', maxWidth: 220, textAlign: 'right' }}>
                your only way in — connect another before removing it
              </span>
            ) : (
              <button onClick={() => disconnect(i)} className="ap-danger-btn" style={{ background: 'none', border: 'none', color: 'var(--faint)', fontSize: 12.5, cursor: 'pointer', padding: '4px 8px', borderRadius: 5 }}>
                Disconnect
              </button>
            )}
          </div>
        ))}
        {identities === null && <div style={{ padding: '13px 18px', color: 'var(--faint)', fontSize: 13 }}>Loading…</div>}
      </div>
      {error && <Notice>{error}</Notice>}
      {notYet.length > 0 && (
        <div style={{ ...card, padding: '14px 18px', marginBottom: 28 }}>
          <div style={{ fontSize: 13, color: 'var(--muted)', marginBottom: 10 }}>
            Connect an account to sign in with it. Any account works — it does not need the same address as this one.
          </div>
          <ProviderButtons providers={notYet} opts={{ link: true }} label="Connect" divider={false} />
        </div>
      )}

      <div style={{ ...colHead, margin: '18px 0 10px' }}>Sessions</div>
      <div style={{ ...card, padding: '16px 20px', display: 'flex', flexDirection: 'column', gap: 12 }}>
        <div style={{ fontSize: 13.5, color: 'var(--muted)' }}>
          Signs you out of every browser and device, this one included. Use it if a laptop went missing or you signed in
          somewhere you should not have.
        </div>
        <div>
          {confirmEverywhere ? (
            <span style={{ display: 'inline-flex', gap: 8 }}>
              <Button tone="danger" onClick={() => onSignOut(true)}>
                Sign out everywhere
              </Button>
              <Button onClick={() => setConfirmEverywhere(false)}>Cancel</Button>
            </span>
          ) : (
            <Button onClick={() => setConfirmEverywhere(true)}>Sign out everywhere</Button>
          )}
        </div>
      </div>
    </div>
  )
}
