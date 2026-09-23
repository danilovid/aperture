// Line icons for the sidebar and menus: 16px, drawn with the current colour so
// they follow the text they sit beside.
import type { ReactNode } from 'react'

export type IconName =
  | 'overview'
  | 'events'
  | 'policies'
  | 'report'
  | 'settings'
  | 'gear'
  | 'playground'
  | 'members'
  | 'tokens'
  | 'audit'
  | 'organization'
  | 'account'
  | 'sun'
  | 'moon'
  | 'signout'
  | 'check'
  | 'chevron'

const paths: Record<IconName, ReactNode> = {
  overview: (
    <>
      <rect x="2.5" y="2.5" width="4.5" height="4.5" rx="1" />
      <rect x="9" y="2.5" width="4.5" height="4.5" rx="1" />
      <rect x="2.5" y="9" width="4.5" height="4.5" rx="1" />
      <rect x="9" y="9" width="4.5" height="4.5" rx="1" />
    </>
  ),
  events: (
    <>
      <path d="M8 1.8 13.2 3.8v4c0 3-2.2 5.2-5.2 6.4C5 13 2.8 10.8 2.8 7.8v-4Z" />
      <path d="M8 5.3v3" />
      <path d="M8 10.6h.01" />
    </>
  ),
  policies: (
    <>
      <path d="M3 4.5h6" />
      <path d="M12 4.5h1" />
      <circle cx="10.5" cy="4.5" r="1.5" />
      <path d="M3 11.5h1" />
      <path d="M7 11.5h6" />
      <circle cx="5.5" cy="11.5" r="1.5" />
    </>
  ),
  report: (
    <>
      <path d="M4 2.5h5.5L12.5 5.5v8H4Z" />
      <path d="M9.5 2.5v3h3" />
      <path d="M6 9h4.5" />
      <path d="M6 11.2h3" />
    </>
  ),
  settings: (
    <>
      <circle cx="5.5" cy="10.5" r="3" />
      <path d="m7.7 8.3 5.3-5.3" />
      <path d="m11 4.9 1.6 1.6" />
      <path d="m9.6 6.3 1.1 1.1" />
    </>
  ),
  gear: (
    <>
      <circle cx="8" cy="8" r="5.3" strokeWidth="2.2" strokeDasharray="2.08 2.08" />
      <circle cx="8" cy="8" r="3.9" />
      <circle cx="8" cy="8" r="1.5" />
    </>
  ),
  playground: <path d="M3 3.5h10v7H7.5L4.5 13v-2.5H3Z" />,
  members: (
    <>
      <circle cx="6" cy="5.5" r="2.3" />
      <path d="M2 13c.4-2.3 2-3.5 4-3.5s3.6 1.2 4 3.5" />
      <path d="M10.5 3.4a2.3 2.3 0 0 1 0 4.2" />
      <path d="M12 9.8c1 .5 1.7 1.6 2 3.2" />
    </>
  ),
  tokens: (
    <>
      <rect x="2.5" y="3.5" width="11" height="9" rx="1.5" />
      <path d="m5 7 1.8 1.5L5 10" />
      <path d="M8.5 10H11" />
    </>
  ),
  audit: (
    <>
      <path d="M2.8 8a5.2 5.2 0 1 0 1.5-3.7" />
      <path d="M2.5 2.5v2.2h2.2" />
      <path d="M8 5.2V8l2 1.3" />
    </>
  ),
  organization: (
    <>
      <path d="M3 13.5V3.5h6v10" />
      <path d="M9 6.5h4v7" />
      <path d="M2 13.5h12" />
      <path d="M5 5.5h2M5 8h2M5 10.5h2" />
    </>
  ),
  account: (
    <>
      <circle cx="8" cy="5.8" r="2.6" />
      <path d="M3 13.5c.6-2.6 2.6-4 5-4s4.4 1.4 5 4" />
    </>
  ),
  sun: (
    <>
      <circle cx="8" cy="8" r="2.8" />
      <path d="M8 1.5v1.6M8 12.9v1.6M1.5 8h1.6M12.9 8h1.6M3.4 3.4l1.1 1.1M11.5 11.5l1.1 1.1M3.4 12.6l1.1-1.1M11.5 4.5l1.1-1.1" />
    </>
  ),
  moon: <path d="M13 9.6A5.5 5.5 0 1 1 6.4 3 4.3 4.3 0 0 0 13 9.6Z" />,
  signout: (
    <>
      <path d="M6.5 13.5H3.5v-11h3" />
      <path d="M10 5l3 3-3 3" />
      <path d="M13 8H6.5" />
    </>
  ),
  check: <path d="m3.5 8.5 3 3 6-6.5" />,
  chevron: <path d="m5 6.5 3 3 3-3" />,
}

export function Icon({ name, size = 16 }: { name: IconName; size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      style={{ flexShrink: 0 }}
    >
      {paths[name]}
    </svg>
  )
}
