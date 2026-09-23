// Which settings pages exist, and who may open which. Kept apart from the
// components so the sidebar and the settings area read the same list.
import { atLeast } from '../api'
import type { Me, Role } from '../api'

export type SettingsSection =
  | 'access'
  | 'keys'
  | 'providers'
  | 'limits'
  | 'alerts'
  | 'general'
  | 'members'
  | 'tokens'
  | 'audit'

interface SectionItem {
  id: SettingsSection
  label: string
  group: 'Gateway' | 'Organization'
  /** Hidden below this role in a signed-in console. */
  min: Role
  /** Only where there are accounts. */
  accountsOnly?: boolean
  /** Only where there are none: the admin key is the console's credential. */
  legacyOnly?: boolean
}

const SECTIONS: SectionItem[] = [
  { id: 'access', label: 'Console access', group: 'Gateway', min: 'admin', legacyOnly: true },
  { id: 'keys', label: 'API keys', group: 'Gateway', min: 'admin' },
  { id: 'providers', label: 'Providers', group: 'Gateway', min: 'admin' },
  { id: 'limits', label: 'Limits', group: 'Gateway', min: 'admin' },
  { id: 'alerts', label: 'Alerts', group: 'Gateway', min: 'admin' },
  { id: 'general', label: 'General', group: 'Organization', min: 'viewer', accountsOnly: true },
  { id: 'members', label: 'Members', group: 'Organization', min: 'viewer', accountsOnly: true },
  { id: 'tokens', label: 'Access tokens', group: 'Organization', min: 'admin', accountsOnly: true },
  { id: 'audit', label: 'Audit log', group: 'Organization', min: 'admin', accountsOnly: true },
]

/** The settings pages this person may open, in menu order. */
export function settingsSections(me?: Me): SectionItem[] {
  return SECTIONS.filter((s) => (me ? !s.legacyOnly && atLeast(me.role, s.min) : !s.accountsOnly))
}
