// Keys the UI uses to talk to the gateway. Both are entered in Settings and
// kept in localStorage; the server logs generated values at startup when the
// APERTURE_API_KEY / ADMIN_API_KEY env vars are not set.

const APERTURE_KEY_STORAGE = 'aperture-api-key'
const ADMIN_KEY_STORAGE = 'aperture-admin-key'

export function getApertureKey(): string {
  return localStorage.getItem(APERTURE_KEY_STORAGE) || ''
}

export function setApertureKey(key: string) {
  localStorage.setItem(APERTURE_KEY_STORAGE, key.trim())
}

export function getAdminKey(): string {
  return localStorage.getItem(ADMIN_KEY_STORAGE) || ''
}

export function setAdminKey(key: string) {
  localStorage.setItem(ADMIN_KEY_STORAGE, key.trim())
}

export function adminHeaders(extra: Record<string, string> = {}): Record<string, string> {
  const key = getAdminKey()
  return key ? { ...extra, Authorization: `Bearer ${key}` } : extra
}
