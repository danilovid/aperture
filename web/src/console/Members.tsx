// The people in this organization, the invitations still out, and the door
// for new ones. The server decides what anybody may do; this screen only
// avoids offering what it would refuse.
import { useCallback, useEffect, useState } from 'react'
import { people, atLeast } from '../api'
import type { CreatedInvitation, Invitation, Me, Member, Role } from '../api'
import { card, colHead, h1Style, mono, subStyle } from './styles'
import { Badge } from './ui'
import { Button, Notice, Select, TextInput } from './forms'
import { fmtTs, timeAgo } from './format'

const ROLES: { value: Role; label: string; what: string }[] = [
  { value: 'viewer', label: 'Viewer', what: 'reads dashboards, the feed and reports' },
  { value: 'member', label: 'Member', what: 'also uses the playground' },
  { value: 'admin', label: 'Admin', what: 'keys, policies, limits, people and tokens' },
  { value: 'owner', label: 'Owner', what: 'everything, including closing the organization' },
]

function RoleBadge({ role }: { role: Role }) {
  const tone =
    role === 'owner'
      ? { bg: 'var(--accent-dim)', fg: 'var(--accent)' }
      : role === 'admin'
        ? { bg: 'var(--green-bg)', fg: 'var(--green)' }
        : { bg: 'var(--bg4)', fg: 'var(--muted)' }
  return <Badge bg={tone.bg} fg={tone.fg}>{role}</Badge>
}

export function Members({ me, toast }: { me: Me; toast: (msg: string) => void }) {
  const myRole = me.role
  const canManage = atLeast(myRole, 'admin')
  const [members, setMembers] = useState<Member[] | null>(null)
  const [invites, setInvites] = useState<Invitation[]>([])
  const [email, setEmail] = useState('')
  const [role, setRole] = useState<Role>('member')
  const [created, setCreated] = useState<CreatedInvitation | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [confirmRemove, setConfirmRemove] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const m = await people.members()
      setMembers(m.members)
    } catch (e) {
      setError((e as Error).message)
    }
    if (canManage) {
      try {
        const i = await people.invitations()
        setInvites(i.invitations.filter((x) => !x.accepted_at))
      } catch {
        setInvites([])
      }
    }
  }, [canManage])

  useEffect(() => {
    void load()
  }, [load])

  // Nobody may hand out more than they have; the server refuses it anyway.
  const grantable = ROLES.filter((r) => atLeast(myRole, r.value))

  const invite = async () => {
    setBusy(true)
    setError('')
    try {
      const inv = await people.invite(email.trim(), role)
      setCreated(inv)
      setEmail('')
      toast(`Invitation for ${inv.email} created`)
      void load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const changeRole = async (m: Member, next: Role) => {
    try {
      await people.setRole(m.id, next)
      toast(`${m.email} is now ${next}`)
      void load()
    } catch (e) {
      toast((e as Error).message)
    }
  }

  const remove = async (m: Member) => {
    try {
      await people.removeMember(m.id)
      setConfirmRemove(null)
      toast(`${m.email} removed`)
      void load()
    } catch (e) {
      toast((e as Error).message)
    }
  }

  const revoke = async (inv: Invitation) => {
    try {
      await people.revokeInvitation(inv.id)
      toast(`Invitation for ${inv.email} revoked`)
      if (created?.id === inv.id) setCreated(null)
      void load()
    } catch (e) {
      toast((e as Error).message)
    }
  }

  const grid = '1.6fr 150px 150px 150px'

  return (
    <div style={{ maxWidth: 900 }}>
      <div style={{ marginBottom: 20 }}>
        <h1 style={h1Style}>Members</h1>
        <div style={subStyle}>People in {me.organization?.name} and what each of them may do</div>
      </div>

      {canManage && (
        <>
          <div style={{ ...colHead, marginBottom: 10 }}>Invite someone</div>
          <div style={{ ...card, padding: '16px 18px', marginBottom: 12, display: 'flex', flexDirection: 'column', gap: 12 }}>
            <form
              onSubmit={(e) => {
                e.preventDefault()
                void invite()
              }}
              style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}
            >
              <TextInput
                type="email"
                required
                placeholder="colleague@company.com"
                aria-label="Email to invite"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                style={{ flex: '1 1 240px', width: 'auto' }}
              />
              <Select value={role} onChange={(e) => setRole(e.target.value as Role)} aria-label="Role">
                {grantable.map((r) => (
                  <option key={r.value} value={r.value}>
                    {r.label}
                  </option>
                ))}
              </Select>
              <Button tone="accent" type="submit" busy={busy} disabled={!email.trim()}>
                Create invitation
              </Button>
            </form>
            <div style={{ fontSize: 12.5, color: 'var(--faint)' }}>
              {ROLES.find((r) => r.value === role)?.label}: {ROLES.find((r) => r.value === role)?.what}. The link works once,
              for this address only, and expires in seven days.
            </div>
            {error && <Notice>{error}</Notice>}
          </div>

          {created && (
            <div style={{ background: 'var(--green-bg)', border: '1px solid var(--green)', borderRadius: 10, padding: '12px 16px', marginBottom: 12, display: 'flex', flexDirection: 'column', gap: 8 }}>
              <div style={{ fontSize: 13 }}>
                <strong style={{ color: 'var(--green)' }}>Send this link to {created.email}.</strong>{' '}
                <span style={{ color: 'var(--muted)' }}>
                  It is shown once — the gateway keeps only a hash of it. There is no email: pass it on however your team
                  shares things.
                </span>
              </div>
              <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                <span style={{ ...mono, fontSize: 12.5, flex: 1, wordBreak: 'break-all' }}>{created.link}</span>
                <Button
                  onClick={async () => {
                    try {
                      await navigator.clipboard.writeText(created.link)
                      toast('Link copied')
                    } catch {
                      toast('Copy failed — select the link and copy it by hand')
                    }
                  }}
                  style={{ padding: '6px 12px', fontSize: 12.5 }}
                >
                  Copy
                </Button>
                <button onClick={() => setCreated(null)} aria-label="Dismiss" style={{ background: 'none', border: 'none', color: 'var(--muted)', fontSize: 16, cursor: 'pointer' }}>
                  ×
                </button>
              </div>
            </div>
          )}

          {invites.length > 0 && (
            <>
              <div style={{ ...colHead, margin: '18px 0 10px' }}>Waiting to be accepted</div>
              <div style={{ ...card, overflow: 'hidden', marginBottom: 24 }}>
                {invites.map((inv) => (
                  <div key={inv.id} className="ap-row-hover" style={{ display: 'grid', gridTemplateColumns: grid, gap: '0 14px', padding: '11px 18px', borderBottom: '1px solid var(--border)', alignItems: 'center' }}>
                    <span style={{ ...mono, fontSize: 13 }}>{inv.email}</span>
                    <span><RoleBadge role={inv.role} /></span>
                    <span style={{ fontSize: 12, color: 'var(--muted)' }}>expires {fmtTs(inv.expires_at).split(',')[0]}</span>
                    <span style={{ textAlign: 'right' }}>
                      <button onClick={() => revoke(inv)} className="ap-danger-btn" style={{ background: 'none', border: 'none', color: 'var(--faint)', fontSize: 12.5, cursor: 'pointer', padding: '4px 8px', borderRadius: 5 }}>
                        Revoke
                      </button>
                    </span>
                  </div>
                ))}
              </div>
            </>
          )}
        </>
      )}

      <div style={{ ...colHead, margin: '18px 0 10px' }}>People</div>
      <div style={{ ...card, overflow: 'hidden' }}>
        <div style={{ display: 'grid', gridTemplateColumns: grid, gap: '0 14px', padding: '9px 18px', borderBottom: '1px solid var(--border)', ...colHead }}>
          <span>Person</span>
          <span>Role</span>
          <span>Last sign-in</span>
          <span />
        </div>
        {members === null ? (
          <div style={{ padding: '22px 18px', color: 'var(--faint)', fontSize: 13 }}>Loading…</div>
        ) : (
          members.map((m) => {
            const isMe = m.id === me.user.id
            // Changing somebody who outranks you is refused by the server;
            // not offering it avoids a button that only ever says no.
            const editable = canManage && !isMe && atLeast(myRole, m.role)
            return (
              <div key={m.id} className="ap-row-hover" style={{ display: 'grid', gridTemplateColumns: grid, gap: '0 14px', padding: '11px 18px', borderBottom: '1px solid var(--border)', alignItems: 'center' }}>
                <span style={{ minWidth: 0 }}>
                  <div style={{ fontSize: 13.5, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {m.name || m.email}
                    {isMe && <span style={{ color: 'var(--faint)', fontWeight: 400 }}> · you</span>}
                  </div>
                  {m.name && <div style={{ ...mono, fontSize: 12, color: 'var(--muted)' }}>{m.email}</div>}
                </span>
                <span>
                  {editable ? (
                    <Select value={m.role} onChange={(e) => changeRole(m, e.target.value as Role)} aria-label={`Role of ${m.email}`} style={{ padding: '5px 8px', fontSize: 12.5 }}>
                      {grantable.map((r) => (
                        <option key={r.value} value={r.value}>
                          {r.label}
                        </option>
                      ))}
                    </Select>
                  ) : (
                    <RoleBadge role={m.role} />
                  )}
                </span>
                <span style={{ fontSize: 12, color: 'var(--muted)' }}>{m.last_login_at ? timeAgo(m.last_login_at) : 'never'}</span>
                <span style={{ textAlign: 'right' }}>
                  {editable &&
                    (confirmRemove === m.id ? (
                      <span style={{ display: 'inline-flex', gap: 6 }}>
                        <Button tone="danger" onClick={() => remove(m)} style={{ padding: '5px 11px', fontSize: 12 }}>
                          Remove
                        </Button>
                        <Button onClick={() => setConfirmRemove(null)} style={{ padding: '5px 10px', fontSize: 12 }}>
                          Cancel
                        </Button>
                      </span>
                    ) : (
                      <button onClick={() => setConfirmRemove(m.id)} className="ap-danger-btn" style={{ background: 'none', border: 'none', color: 'var(--faint)', fontSize: 12.5, cursor: 'pointer', padding: '4px 8px', borderRadius: 5 }}>
                        Remove
                      </button>
                    ))}
                </span>
              </div>
            )
          })
        )}
      </div>
      {!canManage && (
        <div style={{ fontSize: 12.5, color: 'var(--faint)', marginTop: 10 }}>Inviting people and changing roles is for admins.</div>
      )}
    </div>
  )
}
