// The light/dark choice, shared by the landing page, the sign-in pages and the
// console, so moving between them never flips the theme under somebody.
import { useCallback, useState } from 'react'

export type Theme = 'dark' | 'light'
const STORAGE = 'mutegate-theme'

function initial(): Theme {
  try {
    // Or the name it was saved under before the project was renamed.
    const saved = localStorage.getItem(STORAGE) ?? localStorage.getItem('aperture-theme')
    if (saved === 'dark' || saved === 'light') return saved
  } catch {
    // Storage can be unavailable (private mode, blocked site data); the
    // default is a fine answer then.
  }
  // Dark is the design's home; light is one click away and remembered.
  return 'dark'
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
