import { create } from 'zustand'
import { putUIPrefs } from './api'

// Theme is persisted in TWO places:
//   - localStorage('theme') — the fast path, read synchronously at startup so
//     there's no light/dark flash before the daemon status round-trips.
//   - server-side (PUT /ui-prefs) — the durable path, so a desktop webview
//     localStorage wipe (e.g. after a daemon auto-update) doesn't reset the
//     theme. App bootstrap re-applies the server value when localStorage is
//     empty (see App.tsx).

export const THEME_STORAGE_KEY = 'theme'

function initialIsDark(): boolean {
  try {
    const stored = localStorage.getItem(THEME_STORAGE_KEY)
    if (stored) return stored === 'dark'
    return !window.matchMedia?.('(prefers-color-scheme: light)').matches
  } catch {
    return true // default to dark
  }
}

function applyDom(isDark: boolean) {
  document.documentElement.setAttribute('data-theme', isDark ? 'dark' : 'light')
}

interface ThemeState {
  isDark: boolean
  /**
   * Set the theme. `persist` (default true) caches it in localStorage AND
   * writes it server-side — pass false when applying a value that just came
   * FROM the server (App bootstrap) to avoid echoing it straight back.
   */
  setDark: (isDark: boolean, persist?: boolean) => void
  toggle: () => void
}

const init = initialIsDark()
applyDom(init)

export const useThemeStore = create<ThemeState>((set, get) => ({
  isDark: init,
  setDark: (isDark, persist = true) => {
    applyDom(isDark)
    try {
      localStorage.setItem(THEME_STORAGE_KEY, isDark ? 'dark' : 'light')
    } catch {
      /* ignore */
    }
    if (persist) void putUIPrefs({ theme: isDark ? 'dark' : 'light' })
    if (isDark !== get().isDark) set({ isDark })
  },
  toggle: () => get().setDark(!get().isDark, true),
}))
