// The light/dark choice, shared by the landing page, the sign-in pages and the
// console, so moving between them never flips the theme under somebody.
import { useCallback, useState } from 'react'

export type Theme = 'dark' | 'light'
const STORAGE = 'aperture-theme'

function initial(): Theme {
  try {
    const saved = localStorage.getItem(STORAGE)
    if (saved === 'dark' || saved === 'light') return saved
  } catch {
    // Storage can be unavailable (private mode, blocked site data); the
    // system preference is a fine answer then.
  }
  return window.matchMedia?.('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

export function useTheme(): [Theme, () => void] {
  const [theme, setTheme] = useState<Theme>(initial)
  const toggle = useCallback(() => {
    setTheme((t) => {
      const next = t === 'dark' ? 'light' : 'dark'
      try {
        localStorage.setItem(STORAGE, next)
      } catch {
        // Not remembered across reloads; still switched for now.
      }
      return next
    })
  }, [])
  return [theme, toggle]
}
