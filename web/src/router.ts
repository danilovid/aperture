// A router on the History API, and nothing more.
//
// The console has about a dozen routes and no nested layouts, loaders or
// transitions, so a dependency would bring more concepts than routes. What it
// needs is: the current path as React state, a way to change it, and links
// that change it without reloading the page. That is this file.
import { useSyncExternalStore } from 'react'
import type { AnchorHTMLAttributes, MouseEvent } from 'react'
import { createElement } from 'react'

const listeners = new Set<() => void>()

function subscribe(listener: () => void) {
  listeners.add(listener)
  window.addEventListener('popstate', listener)
  return () => {
    listeners.delete(listener)
    window.removeEventListener('popstate', listener)
  }
}

function snapshot() {
  return window.location.pathname + window.location.search
}

/** The current path and query, re-rendering when either changes. */
export function useLocation(): { path: string; query: URLSearchParams } {
  const full = useSyncExternalStore(subscribe, snapshot)
  const [path, search = ''] = full.split('?')
  return { path, query: new URLSearchParams(search) }
}

/**
 * Go somewhere. `replace` is for redirects: the page the visitor was bounced
 * off should not be where Back takes them.
 */
export function navigate(to: string, { replace = false } = {}) {
  if (to === snapshot()) return
  if (replace) window.history.replaceState(null, '', to)
  else window.history.pushState(null, '', to)
  listeners.forEach((l) => l())
  window.scrollTo(0, 0)
}

/**
 * Match a pattern such as "/invite/:token" against a path. Returns the named
 * segments, or null when it does not match. A trailing "*" matches the rest.
 */
export function match(pattern: string, path: string): Record<string, string> | null {
  const p = pattern.split('/').filter(Boolean)
  const s = path.split('/').filter(Boolean)
  const params: Record<string, string> = {}
  for (let i = 0; i < p.length; i++) {
    if (p[i] === '*') {
      params['*'] = s.slice(i).join('/')
      return params
    }
    if (i >= s.length) return null
    if (p[i].startsWith(':')) params[p[i].slice(1)] = decodeURIComponent(s[i])
    else if (p[i] !== s[i]) return null
  }
  return p.length === s.length ? params : null
}

/**
 * An anchor that navigates in place. It is still a real link — middle click,
 * cmd-click and "copy link" all behave — and only a plain left click is
 * intercepted.
 */
export function Link({ to, onClick, ...rest }: AnchorHTMLAttributes<HTMLAnchorElement> & { to: string }) {
  return createElement('a', {
    ...rest,
    href: to,
    onClick: (e: MouseEvent<HTMLAnchorElement>) => {
      onClick?.(e)
      if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
      e.preventDefault()
      navigate(to)
    },
  })
}
