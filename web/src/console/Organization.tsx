// The organization itself: its name, leaving it, and closing it.
import { useEffect, useState } from 'react'
import { auth, people, atLeast } from '../api'
import type { Me } from '../api'
import { card, colHead, h1Style, mono, subStyle } from './styles'
import { Button, Field, Notice, TextInput } from './forms'

export function Organization({
  me,
  onMe,
  onSignedOut,
  toast,
}: {
  me: Me
  onMe: (me: Me) => void
  onSignedOut: () => void
  toast: (msg: string) => void
}) {
  const org = me.organization!
  const canRename = atLeast(me.role, 'admin')
  const isOwner = me.role === 'owner'
  const [name, setName] = useState(org.name)
  const [renaming, setRenaming] = useState(false)
  const [leaveError, setLeaveError] = useState('')
  const [confirmLeave, setConfirmLeave] = useState(false)
  const [slug, setSlug] = useState('')
  const [closing, setClosing] = useState(false)
  const [closeError, setCloseError] = useState('')
  // The last owner cannot leave — the server refuses, so an organization is
  // never left with nobody able to run it. Knowing that up front turns a
  // button that only ever says no into a sentence that says why.
  const [lastOwner, setLastOwner] = useState(false)

  useEffect(() => {
    if (me.role !== 'owner') return
    people
      .members()
      .then((res) => setLastOwner(res.members.filter((m) => m.role === 'owner').length <= 1))
      .catch(() => setLastOwner(false))
  }, [me.role])

  const rename = async () => {
    setRenaming(true)
    try {
      await people.renameOrg(name.trim())
      onMe(await auth.me())
      toast('Organization renamed')
    } catch (e) {
      toast((e as Error).message)
    } finally {
      setRenaming(false)
    }
  }

  const leave = async () => {
    setLeaveError('')
    try {
      const res = await people.leaveOrg()
      if ('user' in res) {
        toast(`You left ${org.name}`)
        onMe(res)
      } else {
        // It was their only organization, and the server has signed them out.
        onSignedOut()
      }
    } catch (e) {
      setLeaveError((e as Error).message)
      setConfirmLeave(false)
    }
  }

  const close = async () => {
    setClosing(true)
    setCloseError('')
    try {
      await people.deleteOrg(slug.trim())
      toast(`${org.name} is closed`)
      // The session's organization is gone; /me says where they are now.
      onMe(await auth.me())
    } catch (e) {
      setCloseError((e as Error).message)
    } finally {
      setClosing(false)
    }
  }

  return (
    <div style={{ maxWidth: 720 }}>
      <div style={{ marginBottom: 20 }}>
        <h1 style={h1Style}>Organization</h1>
        <div style={subStyle}>Everything in this console — keys, incidents, policies, people — belongs to it</div>
      </div>

      <div style={{ ...colHead, marginBottom: 10 }}>Details</div>
      <div style={{ ...card, padding: '18px 20px', marginBottom: 28, display: 'flex', flexDirection: 'column', gap: 16 }}>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void rename()
          }}
          style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}
        >
          <div style={{ flex: 1 }}>
            <Field label="Name">
              <TextInput value={name} onChange={(e) => setName(e.target.value)} readOnly={!canRename} maxLength={200} />
            </Field>
          </div>
          {canRename && (
            <Button type="submit" busy={renaming} disabled={!name.trim() || name.trim() === org.name}>
              Rename
            </Button>
          )}
        </form>
        <Field label="Slug" hint="How the organization is referred to from outside. It does not change when the name does.">
          <TextInput value={org.slug} readOnly style={mono} />
        </Field>
        <div style={{ fontSize: 13, color: 'var(--muted)' }}>
          You are {me.role === 'owner' || me.role === 'admin' ? 'an' : 'a'} <strong style={{ color: 'var(--text)' }}>{me.role}</strong> here.
        </div>
      </div>

      <div style={{ ...colHead, marginBottom: 10 }}>Leave</div>
      <div style={{ ...card, padding: '18px 20px', marginBottom: 28, display: 'flex', flexDirection: 'column', gap: 12 }}>
        {lastOwner ? (
          <div style={{ fontSize: 13.5, color: 'var(--muted)' }}>
            You are the only owner, so you cannot leave: nobody would be left to run {org.name}. Make someone else an owner on
            the Members screen first, or close the organization below.
          </div>
        ) : (
          <div style={{ fontSize: 13.5, color: 'var(--muted)' }}>
            You lose access to {org.name} straight away; someone would have to invite you again.
            {me.organizations.length <= 1 && ' It is your only organization, so you will be signed out.'}
          </div>
        )}
        {leaveError && <Notice>{leaveError}</Notice>}
        {!lastOwner && (
        <div>
          {confirmLeave ? (
            <span style={{ display: 'inline-flex', gap: 8 }}>
              <Button tone="danger" onClick={leave}>
                Leave {org.name}
              </Button>
              <Button onClick={() => setConfirmLeave(false)}>Cancel</Button>
            </span>
          ) : (
            <Button onClick={() => setConfirmLeave(true)}>Leave organization</Button>
          )}
        </div>
        )}
      </div>

      {isOwner && (
        <>
          <div style={{ ...colHead, marginBottom: 10, color: 'var(--red)' }}>Close the organization</div>
          <div style={{ ...card, borderColor: 'var(--red)', padding: '18px 20px', display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div style={{ fontSize: 13.5, color: 'var(--muted)' }}>
              Everything stops at once: the console, every aperture key its agents use, every access token. The data is kept,
              so an operator of this installation can restore it — nobody inside can, because nobody inside can sign in to it.
            </div>
            <Field label={`Type ${org.slug} to confirm`}>
              <TextInput value={slug} onChange={(e) => setSlug(e.target.value)} placeholder={org.slug} style={mono} autoComplete="off" />
            </Field>
            {closeError && <Notice>{closeError}</Notice>}
            <div>
              <Button tone="danger" busy={closing} disabled={slug.trim() !== org.slug} onClick={close}>
                Close {org.name}
              </Button>
            </div>
          </div>
        </>
      )}
    </div>
  )
}
