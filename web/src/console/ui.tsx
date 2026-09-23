// Shared visual atoms for the console, ported from the design system.
import { useId } from 'react'
import type { ReactNode } from 'react'
import { actionStyle, card, mono, provStyle } from './styles'

export function Badge({ bg, fg, children }: { bg: string; fg: string; children: ReactNode }) {
  return (
    <span
      style={{
        fontSize: 10.5,
        fontWeight: 700,
        letterSpacing: 0.6,
        background: bg,
        color: fg,
        padding: '3px 9px',
        borderRadius: 5,
        textTransform: 'uppercase',
        flexShrink: 0,
      }}
    >
      {children}
    </span>
  )
}

export function ActionBadge({ action }: { action: string }) {
  const s = actionStyle(action)
  return <Badge bg={s.bg} fg={s.fg}>{s.label}</Badge>
}

export function ProviderBadge({ provider }: { provider: string }) {
  const s = provStyle(provider)
  return <Badge bg={s.bg} fg={s.fg}>{provider}</Badge>
}

/**
 * The Mutegate mark: two panels of a gate — or the strokes of an M — with a
 * bar laid across the opening like a redaction, and the data that got through
 * leaving on the right. Below 24px the three squares turn to noise, so small
 * sizes draw the gate and the bar alone. `size` is the height; the mark is a
 * little wider than tall.
 */
export function Logo({ size = 24, color = 'var(--accent)' }: { size?: number; color?: string }) {
  const gap = useId().replace(/[^a-zA-Z0-9_-]/g, '')
  const full = size >= 24
  const viewBox = full ? '297 360 750 550' : '297 360 590 550'
  return (
    <svg height={size} viewBox={viewBox} role="img" aria-label="Mutegate" style={{ flexShrink: 0 }}>
      <defs>
        <mask id={gap}>
          <rect x="297" y="360" width="750" height="550" fill="#fff" />
          <rect x="433" y="551" width="470" height="170" rx="48" fill="#000" />
        </mask>
      </defs>
      <g fill={color}>
        <g mask={`url(#${gap})`}>
          <path d="M307 386Q307 370 321 377L485 458Q494 463 494 473L494 797Q494 807 485 812L321 893Q307 900 307 884Z" />
          <path d="M845 386Q845 370 831 377L659 458Q650 463 650 473L650 797Q650 807 659 812L831 893Q845 900 845 884Z" />
        </g>
        <rect x="460" y="578" width="416" height="118" rx="26" />
        {full && (
          <>
            <rect x="905" y="516" width="56" height="56" rx="6" />
            <rect x="981" y="609" width="56" height="56" rx="6" />
            <rect x="905" y="702" width="56" height="56" rx="6" />
          </>
        )}
      </g>
    </svg>
  )
}

export function Segmented<T extends string>({
  value,
  options,
  onChange,
  monoFont,
}: {
  value: T
  options: { value: T; label: string }[]
  onChange: (v: T) => void
  monoFont?: boolean
}) {
  return (
    <div style={{ display: 'flex', gap: 2, background: 'var(--bg3)', borderRadius: 7, padding: 3, width: 'fit-content' }}>
      {options.map((o) => (
        <button
          key={o.value}
          onClick={() => onChange(o.value)}
          style={{
            background: o.value === value ? 'var(--bg4)' : 'none',
            color: o.value === value ? 'var(--text)' : 'var(--muted)',
            border: 'none',
            padding: '5px 12px',
            borderRadius: 5,
            fontSize: 12.5,
            fontWeight: 600,
            cursor: 'pointer',
            ...(monoFont ? mono : {}),
          }}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

export function Toggle({ on, onChange, label }: { on: boolean; onChange: () => void; label: string }) {
  return (
    <button
      onClick={onChange}
      role="switch"
      aria-checked={on}
      aria-label={label}
      style={{
        width: 38,
        height: 22,
        borderRadius: 99,
        border: 'none',
        background: on ? 'var(--accent)' : 'var(--bg4)',
        position: 'relative',
        cursor: 'pointer',
        flexShrink: 0,
        transition: 'background 0.15s',
      }}
    >
      <span
        style={{
          position: 'absolute',
          top: 3,
          left: on ? 19 : 3,
          width: 16,
          height: 16,
          borderRadius: '50%',
          background: '#fff',
          transition: 'left 0.15s',
          display: 'block',
          boxShadow: '0 1px 3px rgba(0,0,0,0.3)',
        }}
      />
    </button>
  )
}

export function EmptyState({ title, sub, tone = 'green' }: { title: string; sub: string; tone?: 'green' | 'muted' }) {
  const color = tone === 'green' ? 'var(--green)' : 'var(--faint)'
  return (
    <div style={{ ...card, padding: '72px 32px', textAlign: 'center' }}>
      <div style={{ marginBottom: 16 }}>
        <Logo size={44} color={color} />
      </div>
      <div style={{ fontWeight: 600, fontSize: 16, marginBottom: 6 }}>{title}</div>
      <div style={{ color: 'var(--muted)', fontSize: 13.5 }}>{sub}</div>
    </div>
  )
}

export function Skeleton({ height, delay = 0 }: { height: number; delay?: number }) {
  return (
    <div
      style={{
        height,
        background: 'var(--bg2)',
        border: '1px solid var(--border)',
        borderRadius: 12,
        animation: `ap-pulse 1.2s ease-in-out infinite`,
        animationDelay: `${delay}s`,
      }}
    />
  )
}
