import { API_URL } from '../api'

/**
 * Where agents reach this gateway: the API's address as the console knows it,
 * made absolute. Behind Caddy or Vite that is this origin; in a split Compose
 * setup it is the gateway's own port; behind the console's nginx, /api.
 */
export function gatewayBase(): string {
  return new URL(API_URL || '/', window.location.origin).href.replace(/\/$/, '')
}
