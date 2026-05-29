import { useEffect } from 'react'

/**
 * Applies the user's persisted theme (`localStorage.theme`) to the current
 * document. Secondary Wails windows don't inherit `data-theme` from the main
 * window's `<html>`, so without this hook the @media (prefers-color-scheme)
 * fallback in `index.css` lights up the whole window when the OS prefers
 * light — even if the user explicitly picked dark in the main window.
 *
 * Use in every `/window/*` route component on mount.
 */
export function useInheritedTheme(): void {
  useEffect(() => {
    try {
      const stored = localStorage.getItem('theme')
      const isDark = stored
        ? stored === 'dark'
        : !window.matchMedia?.('(prefers-color-scheme: light)').matches
      document.documentElement.setAttribute('data-theme', isDark ? 'dark' : 'light')
    } catch {
      document.documentElement.setAttribute('data-theme', 'dark')
    }
  }, [])
}
