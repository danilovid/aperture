// Form atoms shared by the sign-in pages and the console's people screens.
import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode, SelectHTMLAttributes } from 'react'

const fieldStyle = {
  background: 'var(--bg)',
  border: '1px solid var(--border)',
  borderRadius: 7,
  padding: '9px 12px',
  fontSize: 13.5,
  color: 'var(--text)',
  width: '100%',
  boxSizing: 'border-box',
} as const

export function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--muted)' }}>{label}</span>
      {children}
      {hint && <span style={{ fontSize: 12, color: 'var(--faint)' }}>{hint}</span>}
    </label>
  )
}

export function TextInput(props: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      {...props}
      style={{
        ...fieldStyle,
        ...(props.readOnly ? { background: 'var(--bg3)', color: 'var(--muted)' } : {}),
        ...props.style,
      }}
    />
  )
}

export function Select(props: SelectHTMLAttributes<HTMLSelectElement>) {
  return <select {...props} style={{ ...fieldStyle, width: 'auto', cursor: 'pointer', ...props.style }} />
}

type Tone = 'accent' | 'plain' | 'danger'

export function Button({
  tone = 'plain',
  busy,
  children,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { tone?: Tone; busy?: boolean }) {
  const look: Record<Tone, React.CSSProperties> = {
    accent: { background: 'var(--accent)', color: 'var(--on-accent)', border: '1px solid var(--accent)' },
    plain: { background: 'var(--bg3)', color: 'var(--text)', border: '1px solid var(--border2)' },
    danger: { background: 'var(--red)', color: '#fff', border: '1px solid var(--red)' },
  }
  const disabled = rest.disabled || busy
  return (
    <button
      {...rest}
      disabled={disabled}
      className={tone === 'accent' ? 'ap-accent-btn' : tone === 'plain' ? 'ap-save-btn' : undefined}
      style={{
        ...look[tone],
        padding: '9px 16px',
        borderRadius: 7,
        fontSize: 13.5,
        fontWeight: 600,
        cursor: disabled ? 'default' : 'pointer',
        opacity: disabled ? 0.6 : 1,
        whiteSpace: 'nowrap',
        ...rest.style,
      }}
    >
      {busy ? '…' : children}
    </button>
  )
}

/** A line of feedback under a form: an error, or a quiet note. */
export function Notice({ tone = 'error', children }: { tone?: 'error' | 'ok' | 'warn'; children: ReactNode }) {
  const c = {
    error: { bg: 'var(--red-bg)', fg: 'var(--red)' },
    ok: { bg: 'var(--green-bg)', fg: 'var(--green)' },
    warn: { bg: 'var(--amber-bg)', fg: 'var(--amber)' },
  }[tone]
  return (
    <div role={tone === 'error' ? 'alert' : 'status'} style={{ background: c.bg, color: c.fg, borderRadius: 8, padding: '10px 13px', fontSize: 13 }}>
      {children}
    </div>
  )
}
