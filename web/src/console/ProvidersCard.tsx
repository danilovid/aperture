// The organization's upstreams: where each one is, which key it uses, and
// which proxy its traffic leaves through. Secrets are write-only here — the
// console is shown whether a key or proxy is set, never its value.
import { useCallback, useEffect, useState } from 'react'
import { providers as providersApi } from '../api'
import type { ProbeResult, ProviderKind, ProviderSave, ProviderView } from '../api'
import { card, mono, provStyle } from './styles'
import { Badge, Toggle } from './ui'
import { Button, Field, Notice, TextInput } from './forms'

const kindName: Record<ProviderKind, string> = {
  openai: 'OpenAI',
  anthropic: 'Anthropic',
  groq: 'Groq',
  jev: 'Jev',
  'openai-compatible': 'OpenAI-compatible',
}

interface Draft {
  name: string
  kind: ProviderKind
  isNew: boolean
  base_url: string
  /** Typed key; undefined means "leave the stored one alone". */
  api_key?: string
  proxy_url?: string
  prefixes: string
  timeout_s: string
  enabled: boolean
}

function draftOf(p: ProviderView): Draft {
  return {
    name: p.name,
    kind: p.kind,
    isNew: false,
    base_url: p.base_url ?? '',
    prefixes: (p.prefixes ?? []).join(', '),
    timeout_s: p.timeout_ms ? String(p.timeout_ms / 1000) : '',
    enabled: p.enabled,
  }
}

/** Only what changed goes to the server; absent fields keep their values. */
function saveOf(d: Draft): ProviderSave {
  const body: ProviderSave = { kind: d.kind, base_url: d.base_url.trim(), enabled: d.enabled }
  if (d.api_key !== undefined) body.api_key = d.api_key
  if (d.proxy_url !== undefined) body.proxy_url = d.proxy_url
  if (d.kind === 'openai-compatible') {
    body.prefixes = d.prefixes.split(',').map((s) => s.trim()).filter(Boolean)
  }
  const secs = Number(d.timeout_s)
  body.timeout_ms = d.timeout_s.trim() === '' || !Number.isFinite(secs) ? 0 : Math.round(secs * 1000)
  return body
}

function ProbeLine({ r }: { r: ProbeResult }) {
  return (
    <Notice tone={r.ok ? 'ok' : 'error'}>
      <div>{r.message}</div>
      <div style={{ ...mono, fontSize: 11.5, opacity: 0.85, marginTop: 4 }}>
        {r.target}
        {r.via ? ` · via ${r.via}` : ' · direct'} · {r.latency_ms} ms · stage: {r.stage}
      </div>
    </Notice>
  )
}

function Editor({
  draft,
  current,
  onChange,
  onSaved,
  onCancel,
  toast,
}: {
  draft: Draft
  current?: ProviderView
  onChange: (d: Draft) => void
  onSaved: () => void
  onCancel: () => void
  toast: (msg: string) => void
}) {
  const [busy, setBusy] = useState<'save' | 'test' | 'delete' | null>(null)
  const [error, setError] = useState('')
  const [probe, setProbe] = useState<ProbeResult | null>(null)
  const set = (patch: Partial<Draft>) => onChange({ ...draft, ...patch })
  const compatible = draft.kind === 'openai-compatible'

  const save = async () => {
    setBusy('save')
    setError('')
    try {
      await providersApi.save(draft.name.trim().toLowerCase(), saveOf(draft))
      toast(`${draft.name} saved`)
      onSaved()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(null)
    }
  }

  const test = async () => {
    setBusy('test')
    setError('')
    setProbe(null)
    try {
      setProbe(await providersApi.test(draft.name.trim().toLowerCase(), saveOf(draft)))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(null)
    }
  }

  const remove = async () => {
    setBusy('delete')
    try {
      await providersApi.remove(draft.name)
      toast(`${draft.name} removed`)
      onSaved()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(null)
    }
  }

  return (
    <div style={{ padding: '16px 18px', display: 'flex', flexDirection: 'column', gap: 12, background: 'var(--bg3)', borderTop: '1px solid var(--border)' }}>
      {compatible && draft.isNew && (
        <Field label="Name" hint="Lowercase letters, digits and dashes. Mutegate keys file their own keys for it under this name.">
          <TextInput value={draft.name} onChange={(e) => set({ name: e.target.value })} placeholder="deepseek" style={mono} />
        </Field>
      )}
      <Field
        label="Address"
        hint={compatible ? 'Including any version path, e.g. https://api.deepseek.com/v1' : `Empty means the default: ${current?.effective_url ?? 'the documented address'}`}
      >
        <TextInput value={draft.base_url} onChange={(e) => set({ base_url: e.target.value })} placeholder={compatible ? 'https://…/v1' : current?.effective_url} style={mono} />
      </Field>
      <Field
        label="API key"
        hint={
          current?.key_set
            ? `Set (${current.key_hint}). Type to replace it; Mutegate keys with their own key for this provider use theirs.`
            : 'The organization’s key: every Mutegate key without its own uses it.'
        }
      >
        <div style={{ display: 'flex', gap: 8 }}>
          <TextInput
            type="password"
            autoComplete="off"
            value={draft.api_key ?? ''}
            onChange={(e) => set({ api_key: e.target.value })}
            placeholder={current?.key_set ? '•••••••• (set)' : 'not set'}
            style={{ ...mono, flex: 1 }}
          />
          {current?.key_set && draft.api_key === undefined && (
            <Button type="button" onClick={() => set({ api_key: '' })} style={{ padding: '6px 12px', fontSize: 12.5 }}>
              Clear
            </Button>
          )}
        </div>
      </Field>
      <Field
        label="Proxy"
        hint={
          current?.proxy
            ? `Now: ${current.proxy}. Type a new address to replace it.`
            : 'http://, https:// or socks5://, with user:password@ if the proxy needs them. Empty means direct, or HTTP_PROXY from the environment.'
        }
      >
        <div style={{ display: 'flex', gap: 8 }}>
          <TextInput
            autoComplete="off"
            value={draft.proxy_url ?? ''}
            onChange={(e) => set({ proxy_url: e.target.value })}
            placeholder={current?.proxy ?? 'direct'}
            style={{ ...mono, flex: 1 }}
          />
          {current?.proxy && draft.proxy_url === undefined && (
            <Button type="button" onClick={() => set({ proxy_url: '' })} style={{ padding: '6px 12px', fontSize: 12.5 }}>
              Remove
            </Button>
          )}
        </div>
      </Field>
      {compatible && (
        <Field label="Model prefixes" hint="Comma-separated. A model starting with one of these goes here; the longest match wins.">
          <TextInput value={draft.prefixes} onChange={(e) => set({ prefixes: e.target.value })} placeholder="deepseek-" style={mono} />
        </Field>
      )}
      <Field label="Time to first byte, seconds" hint="Empty means the default, 120 seconds.">
        <TextInput value={draft.timeout_s} onChange={(e) => set({ timeout_s: e.target.value })} placeholder="120" style={{ ...mono, width: 120 }} />
      </Field>

      {probe && <ProbeLine r={probe} />}
      {error && <Notice>{error}</Notice>}

      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <Button tone="accent" onClick={save} busy={busy === 'save'} disabled={busy !== null || !draft.name.trim()}>
          Save
        </Button>
        <Button onClick={test} busy={busy === 'test'} disabled={busy !== null || !draft.name.trim()}>
          Test connection
        </Button>
        <Button onClick={onCancel} disabled={busy !== null}>
          Cancel
        </Button>
        <span style={{ flex: 1 }} />
        {!draft.isNew && (
          <Button tone="danger" onClick={remove} busy={busy === 'delete'} disabled={busy !== null}>
            Remove
          </Button>
        )}
      </div>
    </div>
  )
}

/**
 * Shown when the gateway has a database. `onUnavailable` is called when it
 * turns out not to, so Settings can fall back to the old key form.
 */
export function ProvidersCard({ toast, onUnavailable }: { toast: (msg: string) => void; onUnavailable: () => void }) {
  const [list, setList] = useState<ProviderView[] | null>(null)
  const [available, setAvailable] = useState<{ kind: ProviderKind; effective_url: string }[]>([])
  const [editing, setEditing] = useState<Draft | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await providersApi.list()
      setList(res.providers)
      setAvailable(res.available ?? [])
    } catch (e) {
      if ((e as { status?: number }).status === 503) onUnavailable()
      setList([])
    }
  }, [onUnavailable])

  useEffect(() => {
    // load() only sets state after its awaited call resolves.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load()
  }, [load])

  const toggle = async (p: ProviderView) => {
    try {
      await providersApi.save(p.name, { enabled: !p.enabled })
      toast(`${p.name} ${p.enabled ? 'disabled' : 'enabled'}`)
      void load()
    } catch (e) {
      toast((e as Error).message)
    }
  }

  const add = (kind: ProviderKind) =>
    setEditing({
      name: kind === 'openai-compatible' ? '' : kind,
      kind,
      isNew: true,
      base_url: '',
      prefixes: '',
      timeout_s: '',
      enabled: true,
    })

  const done = () => {
    setEditing(null)
    void load()
  }

  const editor = (current?: ProviderView) =>
    editing && (
      <Editor
        draft={editing}
        current={current}
        onChange={setEditing}
        onSaved={done}
        onCancel={() => setEditing(null)}
        toast={toast}
      />
    )

  return (
    <>
      <div style={{ ...card, overflow: 'hidden', marginBottom: 12 }}>
        {list === null && <div style={{ padding: '16px 18px', color: 'var(--faint)', fontSize: 13 }}>Loading…</div>}
        {list?.length === 0 && !editing && (
          <div style={{ padding: '16px 18px', color: 'var(--faint)', fontSize: 13 }}>
            No providers yet. Add one below — its key becomes the default for every Mutegate key in this organization.
          </div>
        )}
        {list?.map((p) => {
          const s = provStyle(p.kind === 'openai-compatible' ? p.name : p.kind)
          const open = editing && !editing.isNew && editing.name === p.name
          return (
            <div key={p.name} style={{ borderBottom: '1px solid var(--border)' }}>
              <div className="ap-row-hover" style={{ display: 'flex', alignItems: 'center', gap: 14, padding: '12px 18px', opacity: p.enabled ? 1 : 0.6 }}>
                <span style={{ width: 110, flexShrink: 0 }}>
                  <Badge bg={s.bg} fg={s.fg}>{p.name}</Badge>
                </span>
                <span style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ ...mono, fontSize: 12.5, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.effective_url}</div>
                  <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 2 }}>
                    {p.key_set ? `key ${p.key_hint}` : 'no key'}
                    {' · '}
                    {p.proxy ? `via ${p.proxy}` : 'direct'}
                    {p.prefixes?.length ? ` · ${p.prefixes.join(', ')}` : ''}
                  </div>
                </span>
                <Toggle on={p.enabled} onChange={() => toggle(p)} label={`${p.name} enabled`} />
                <Button onClick={() => setEditing(open ? null : draftOf(p))} style={{ padding: '6px 12px', fontSize: 12.5 }}>
                  {open ? 'Close' : 'Edit'}
                </Button>
              </div>
              {open && editor(p)}
            </div>
          )
        })}
        {editing?.isNew && (
          <div>
            <div style={{ padding: '12px 18px', fontSize: 13, fontWeight: 600 }}>New {kindName[editing.kind]} provider</div>
            {editor(undefined)}
          </div>
        )}
      </div>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 30 }}>
        {available.map((a) => (
          <Button key={a.kind} onClick={() => add(a.kind)} disabled={!!editing} style={{ padding: '6px 12px', fontSize: 12.5 }}>
            + {kindName[a.kind]}
          </Button>
        ))}
        <Button onClick={() => add('openai-compatible')} disabled={!!editing} style={{ padding: '6px 12px', fontSize: 12.5 }}>
          + OpenAI-compatible
        </Button>
      </div>
    </>
  )
}
