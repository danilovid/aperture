// Keys the UI uses to talk to the gateway. Both are entered in Settings and
// kept in localStorage; the server logs generated values at startup when the
// MUTEGATE_API_KEY / ADMIN_API_KEY env vars are not set.

const MUTEGATE_KEY_STORAGE = 'mutegate-api-key'
const ADMIN_KEY_STORAGE = 'mutegate-admin-key'

/**
 * A value saved under its new name, or under the name it had before the
 * project was renamed — so nobody has to paste their keys in again.
 */
export function readSaved(key: string, legacyKey: string): string {
  return localStorage.getItem(key) ?? localStorage.getItem(legacyKey) ?? ''
}

export function getMutegateKey(): string {
  return readSaved(MUTEGATE_KEY_STORAGE, 'aperture-api-key')
}

export function setMutegateKey(key: string) {
  localStorage.setItem(MUTEGATE_KEY_STORAGE, key.trim())
}

export function getAdminKey(): string {
  return readSaved(ADMIN_KEY_STORAGE, 'aperture-admin-key')
}

export function setAdminKey(key: string) {
  localStorage.setItem(ADMIN_KEY_STORAGE, key.trim())
}

export function adminHeaders(extra: Record<string, string> = {}): Record<string, string> {
  const key = getAdminKey()
  return key ? { ...extra, Authorization: `Bearer ${key}` } : extra
}
