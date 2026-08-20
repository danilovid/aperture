import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '../api'
import type { ApertureKey, DryRunResult, Policy, PolicyAction } from '../api'
import { ActionBadge, Segmented, Toggle } from './ui'
import { card, colHead, h1Style, mono, subStyle } from './styles'

type GroupName = 'secrets' | 'pii' | 'custom'

const groupMeta: { id: GroupName; name: string; desc: string; defaultOn: PolicyAction }[] = [
  { id: 'secrets', name: 'Secrets', desc: 'AWS keys, GitHub / GitLab / Slack tokens, PEM private keys, JWTs, generic credentials', defaultOn: 'block' },
  { id: 'pii', name: 'PII', desc: 'Emails, payment cards, phone numbers, IBAN account numbers', defaultOn: 'redact' },
  { id: 'custom', name: 'Custom rules', desc: 'Your regexes and stop-words (configured below)', defaultOn: 'alert' },
]

const inputStyle = {
  background: 'var(--bg)',
  border: '1px solid var(--border)',
  borderRadius: 7,
  padding: '8px 12px',
  fontSize: 13,
  fontFamily: "'IBM Plex Mono', monospace",
  color: 'var(--text)',
} as const

const DEFAULT_TAB = '__default__'

export function Policies({ toast }: { toast: (msg: string) => void }) {
  const [target, setTarget] = useState(DEFAULT_TAB)
  const [keys, setKeys] = useState<ApertureKey[]>([])
  const [boundKeys, setBoundKeys] = useState<Record<string, Policy>>({})
  const [policy, setPolicy] = useState<Policy | null>(null)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [newRuleName, setNewRuleName] = useState('')
  const [newRulePattern, setNewRulePattern] = useState('')
  const [newAllow, setNewAllow] = useState('')
  const [previewText, setPreviewText] = useState(
    'Deploy notes: use AKIAIOSFODNN7EXAMPLE for staging,\nping ivan.petrov@corp.io about the rollout.',
  )
  const [preview, setPreview] = useState<DryRunResult | null>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout>>(undefined)

  const load = useCallback(async () => {
    const [{ default: def, keys: bound }, keysRes] = await Promise.all([
      api.policies(),
      api.listKeys().catch(() => ({ keys: [] as ApertureKey[] })),
    ])
    setBoundKeys(bound)
    setKeys(keysRes.keys)
    setPolicy(target === DEFAULT_TAB ? def : bound[target] ?? def)
    setDirty(false)
  }, [target])

  useEffect(() => {
    load().catch(() => {})
  }, [load])

  // Live dry-run against the *unsaved* policy state.
  useEffect(() => {
    if (!policy) return
    clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => {
      api
        .testPolicy(previewText, policy)
        .then(setPreview)
        .catch(() => setPreview(null))
    }, 350)
    return () => clearTimeout(debounceRef.current)
  }, [previewText, policy])

  const update = (p: Policy) => {
    setPolicy(p)
    setDirty(true)
  }

  const save = async () => {
    if (!policy) return
    setSaving(true)
    try {
      if (target === DEFAULT_TAB) {
        await api.putDefaultPolicy(policy)
      } else {
        await api.putKeyPolicy(target, policy)
      }
      setDirty(false)
      toast('Policy saved — applied to live traffic')
      load().catch(() => {})
    } catch (e) {
      toast(`Save failed: ${(e as Error).message}`)
    } finally {
      setSaving(false)
    }
  }

  const revertKey = async () => {
    try {
      await api.deleteKeyPolicy(target)
      toast('Key reverted to the default policy')
      load().catch(() => {})
    } catch (e) {
      toast(`Revert failed: ${(e as Error).message}`)
    }
  }

  if (!policy) return null

  const rules = policy.custom_rules ?? []
  const allows = policy.allowlist ?? []
  const muted = policy.muted_rules ?? []

  return (
    <div>
      <div style={{ marginBottom: 20 }}>
        <h1 style={h1Style}>Policies</h1>
        <div style={subStyle}>What each key is allowed to send upstream — changes apply immediately</div>
      </div>

      <div style={{ display: 'flex', gap: 8, marginBottom: 20, flexWrap: 'wrap', alignItems: 'center' }}>
        {[{ id: DEFAULT_TAB, name: 'default' }, ...keys.map((k) => ({ id: k.id, name: k.name || k.id }))].map((t) => {
          const active = target === t.id
          const bound = t.id !== DEFAULT_TAB && boundKeys[t.id]
          return (
            <button
              key={t.id}
              onClick={() => setTarget(t.id)}
              className="ap-pill"
              style={{
                ...mono,
                fontSize: 12.5,
                background: active ? 'var(--accent-dim)' : 'var(--bg2)',
                color: active ? 'var(--accent)' : 'var(--muted)',
                border: `1px solid ${active ? 'var(--accent)' : 'var(--border)'}`,
                padding: '6px 14px',
                borderRadius: 99,
                cursor: 'pointer',
                fontWeight: 500,
              }}
            >
              {t.name}
              {bound ? ' •' : ''}
            </button>
          )
        })}
        <div style={{ flex: 1 }} />
        {target !== DEFAULT_TAB && boundKeys[target] && (
          <button
            onClick={revertKey}
            className="ap-danger-btn"
            style={{ background: 'none', border: 'none', color: 'var(--faint)', fontSize: 12.5, cursor: 'pointer', padding: '4px 8px', borderRadius: 5 }}
          >
            Revert to default
          </button>
        )}
        <button
          onClick={save}
          disabled={!dirty || saving}
          className="ap-accent-btn"
          style={{
            background: dirty ? 'var(--accent)' : 'var(--bg3)',
            color: dirty ? '#0b0e13' : 'var(--faint)',
            border: 'none',
            padding: '8px 18px',
            borderRadius: 7,
            fontSize: 13,
            fontWeight: 600,
            cursor: dirty ? 'pointer' : 'default',
          }}
        >
          {saving ? 'Saving…' : dirty ? 'Save policy' : 'Saved'}
        </button>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1.4fr 1fr', gap: 20, alignItems: 'flex-start' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          {groupMeta.map((g) => {
            const action = policy[g.id]
            const on = action !== 'off' && action !== undefined
            return (
              <div key={g.id} style={{ ...card, padding: '18px 20px' }}>
                <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 16 }}>
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontWeight: 600, marginBottom: 3 }}>{g.name}</div>
                    <div style={{ color: 'var(--muted)', fontSize: 13 }}>{g.desc}</div>
                  </div>
                  <Toggle
                    on={on}
                    label={g.name}
                    onChange={() => update({ ...policy, [g.id]: on ? 'off' : g.defaultOn })}
                  />
                </div>
                {on && (
                  <div style={{ marginTop: 14 }}>
                    <Segmented
                      value={action as 'block' | 'redact' | 'alert'}
                      onChange={(a) => update({ ...policy, [g.id]: a })}
                      options={[
                        { value: 'block', label: 'Block' },
                        { value: 'redact', label: 'Redact' },
                        { value: 'alert', label: 'Alert only' },
                      ]}
                    />
                  </div>
                )}
              </div>
            )
          })}

              <div style={{ ...card, padding: '18px 20px' }}>
            <div style={{ fontWeight: 600, marginBottom: 3 }}>Allowlist</div>
            <div style={{ color: 'var(--muted)', fontSize: 13, marginBottom: 14 }}>
              Values matching these patterns never raise a finding — for known false positives
              like AWS's documented example key. Matches are still recorded as muted.
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginBottom: allows.length ? 12 : 0 }}>
              {allows.map((a, i) => (
                <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 12, background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 8, padding: '9px 14px' }}>
                  <span style={{ ...mono, fontSize: 12, color: 'var(--muted)', flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a}</span>
                  <button
                    onClick={() => update({ ...policy, allowlist: allows.filter((_, j) => j !== i) })}
                    aria-label="Remove allowlist entry"
                    className="ap-danger-btn"
                    style={{ background: 'none', border: 'none', color: 'var(--faint)', cursor: 'pointer', fontSize: 15, padding: '0 4px', borderRadius: 4 }}
                  >
                    ×
                  </button>
                </div>
              ))}
            </div>
            <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
              <input
                value={newAllow}
                onChange={(e) => setNewAllow(e.target.value)}
                placeholder="value or regex (e.g. AKIAIOSFODNN7EXAMPLE)"
                aria-label="Allowlist pattern"
                style={{ ...inputStyle, flex: 1 }}
              />
              <button
                onClick={() => {
                  if (!newAllow.trim()) return
                  update({ ...policy, allowlist: [...allows, newAllow.trim()] })
                  setNewAllow('')
                }}
                className="ap-accent-btn"
                style={{ background: 'var(--accent)', color: '#0b0e13', border: 'none', padding: '8px 18px', borderRadius: 7, fontSize: 13, fontWeight: 600, cursor: 'pointer' }}
              >
                Add
              </button>
            </div>
          </div>

          {muted.length > 0 && (
            <div style={{ ...card, padding: '18px 20px' }}>
              <div style={{ fontWeight: 600, marginBottom: 3 }}>Muted detectors</div>
              <div style={{ color: 'var(--muted)', fontSize: 13, marginBottom: 14 }}>
                Silenced from the incident feed. Their matches are still recorded as muted.
              </div>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                {muted.map((m) => (
                  <span key={m} style={{ display: 'inline-flex', alignItems: 'center', gap: 8, background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 99, padding: '5px 8px 5px 12px' }}>
                    <span style={{ ...mono, fontSize: 12 }}>{m}</span>
                    <button
                      onClick={() => update({ ...policy, muted_rules: muted.filter((x) => x !== m) })}
                      aria-label={`Un-mute ${m}`}
                      className="ap-danger-btn"
                      style={{ background: 'none', border: 'none', color: 'var(--faint)', cursor: 'pointer', fontSize: 14, padding: '0 2px', borderRadius: 4 }}
                    >
                      ×
                    </button>
                  </span>
                ))}
              </div>
            </div>
          )}

      <div style={{ ...card, padding: '18px 20px' }}>
            <div style={{ fontWeight: 600, marginBottom: 3 }}>Custom rules</div>
            <div style={{ color: 'var(--muted)', fontSize: 13, marginBottom: 14 }}>Regexes and stop-words specific to your projects</div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginBottom: rules.length ? 12 : 0 }}>
              {rules.map((r, i) => (
                <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 12, background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 8, padding: '9px 14px' }}>
                  <span style={{ ...mono, fontSize: 12.5, color: 'var(--accent)', flexShrink: 0 }}>custom:{r.name}</span>
                  <span style={{ ...mono, fontSize: 12, color: 'var(--muted)', flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{r.pattern}</span>
                  <button
                    onClick={() => update({ ...policy, custom_rules: rules.filter((_, j) => j !== i) })}
                    aria-label="Delete rule"
                    className="ap-danger-btn"
                    style={{ background: 'none', border: 'none', color: 'var(--faint)', cursor: 'pointer', fontSize: 15, padding: '0 4px', borderRadius: 4 }}
                  >
                    ×
                  </button>
                </div>
              ))}
            </div>
            <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
              <input value={newRuleName} onChange={(e) => setNewRuleName(e.target.value)} placeholder="name (e.g. project-x)" aria-label="Rule name" style={{ ...inputStyle, flex: '0 0 180px' }} />
              <input value={newRulePattern} onChange={(e) => setNewRulePattern(e.target.value)} placeholder="regex or stop-word" aria-label="Rule pattern" style={{ ...inputStyle, flex: 1 }} />
              <button
                onClick={() => {
                  if (!newRuleName.trim() || !newRulePattern.trim()) return
                  update({ ...policy, custom_rules: [...rules, { name: newRuleName.trim(), pattern: newRulePattern.trim() }] })
                  setNewRuleName('')
                  setNewRulePattern('')
                }}
                className="ap-accent-btn"
                style={{ background: 'var(--accent)', color: '#0b0e13', border: 'none', padding: '8px 18px', borderRadius: 7, fontSize: 13, fontWeight: 600, cursor: 'pointer' }}
              >
                Add
              </button>
            </div>
          </div>
        </div>

        <div style={{ ...card, padding: '18px 20px', position: 'sticky', top: 28 }}>
          <div style={{ fontWeight: 600, marginBottom: 3 }}>Test this policy</div>
          <div style={{ color: 'var(--muted)', fontSize: 13, marginBottom: 14 }}>Paste sample text — see what would happen before it ships</div>
          <textarea
            value={previewText}
            onChange={(e) => setPreviewText(e.target.value)}
            rows={5}
            aria-label="Sample text"
            style={{ width: '100%', background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 8, padding: '12px 14px', ...mono, fontSize: 12.5, color: 'var(--text)', resize: 'vertical', lineHeight: 1.6, boxSizing: 'border-box' }}
          />
          <div style={{ margin: '14px 0 8px', ...colHead }}>Verdict</div>
          {preview && preview.findings.length === 0 && preview.suppressed.length === 0 ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: 9, background: 'var(--green-bg)', borderRadius: 8, padding: '11px 14px', fontSize: 13, color: 'var(--green)', fontWeight: 600 }}>
              <span style={{ width: 7, height: 7, borderRadius: '50%', background: 'var(--green)', display: 'inline-block' }} />
              Clean — request passes through
            </div>
          ) : preview && preview.findings.length === 0 ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: 9, background: 'var(--bg3)', borderRadius: 8, padding: '11px 14px', fontSize: 13, color: 'var(--muted)' }}>
              <span style={{ width: 7, height: 7, borderRadius: '50%', background: 'var(--faint)', display: 'inline-block' }} />
              {preview.suppressed.length} match{preview.suppressed.length > 1 ? 'es' : ''} suppressed — request passes through
            </div>
          ) : preview ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              {preview.findings.map((f, i) => (
                <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 10, background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 8, padding: '9px 12px' }}>
                  <ActionBadge action={f.action} />
                  <span style={{ ...mono, fontSize: 12 }}>{f.rule}</span>
                  <span style={{ ...mono, fontSize: 11.5, color: 'var(--faint)', flex: 1, textAlign: 'right', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{f.masked_sample}</span>
                </div>
              ))}
              {preview.suppressed?.length > 0 && (
                <div style={{ marginTop: 4, fontSize: 12, color: 'var(--faint)' }}>
                  {preview.suppressed.length} match{preview.suppressed.length > 1 ? 'es' : ''} suppressed
                  by the allowlist or a mute
                </div>
              )}
              <div style={{ marginTop: 8, ...colHead }}>What the provider would receive</div>
              <div style={{ ...mono, fontSize: 12, background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 8, padding: '11px 13px', color: 'var(--muted)', whiteSpace: 'pre-wrap', wordBreak: 'break-word', lineHeight: 1.6 }}>
                {preview.verdict === 'block' ? '— request blocked, nothing sent —' : preview.upstream_text}
              </div>
            </div>
          ) : (
            <div style={{ color: 'var(--faint)', fontSize: 13 }}>…</div>
          )}
        </div>
      </div>
    </div>
  )
}
