import { hasDesktopWindowManager, openDesktopWindow, getSystemDump, saveSystemDumpNative } from './api'
import { usePanelStore, type BottomPanelTab } from './panels'

/** Returns true if the daemon health message looks like "docker stopped" vs "docker missing". */
export function detectInstalledButStopped(message: string): boolean {
  const m = message.toLowerCase()
  return m.includes('connection refused') ||
    m.includes('connection reset') ||
    m.includes('no such file') ||
    m.includes('cannot connect') ||
    m.includes('dial unix')
}

/**
 * Detection of "desktop mode" runs once per page load and is cached. It hits
 * /desktop/windows/list which is only mounted by the Wails desktop binary, so
 * a 404 / network error means we are in server mode.
 *
 * We expose two layers:
 *   - {@link primeDesktopModeCache} — fire-and-forget that fills `cachedSync`.
 *   - {@link isDesktopModeSync} — sync read of the cached value (assumes
 *     server mode until proven otherwise so the initial click does the right
 *     thing in browsers).
 */
let cached: Promise<boolean> | null = null
let cachedSync = false

export function primeDesktopModeCache(): Promise<boolean> {
  if (cached == null) {
    cached = hasDesktopWindowManager().then((v) => {
      cachedSync = v
      return v
    })
  }
  return cached
}

export function isDesktopModeSync(): boolean {
  if (cached == null) primeDesktopModeCache()
  return cachedSync
}

/** Resets the cache. Tests rely on this between cases. */
export function resetDesktopModeCache() {
  cached = null
  cachedSync = false
}

/**
 * Exports a system dump. Returns the saved file path in desktop mode (where
 * the Wails layer writes the ZIP to disk and opens its folder), or null in
 * server mode (where the browser downloads it). The browser <a download> path
 * does nothing inside the WebKitGTK webview, so desktop routes to the native
 * endpoint instead. Awaits the desktop-mode probe rather than the sync cache
 * so a dump triggered before the probe resolves still picks the right path.
 */
export async function triggerSystemDump(): Promise<string | null> {
  if (await primeDesktopModeCache()) {
    return saveSystemDumpNative()
  }
  await getSystemDump('zip')
  return null
}

/**
 * Tabs that map cleanly to a separate OS window when desktop mode is
 * available. Other tab types (restart-events) stay in the bottom panel
 * because they share state with the main window UX.
 */
const WINDOWABLE_TYPES: BottomPanelTab['type'][] = ['logs', 'app-config', 'daemon-logs']

/**
 * Opens a secondary view. In desktop mode the mapped tab types spawn a
 * separate OS window; otherwise the legacy bottom panel is used. The function
 * is intentionally synchronous so React event handlers do not race against
 * a Promise — the first call may use stale cache (defaults to server mode)
 * but desktop detection happens during the initial page load so by the time
 * any user click happens, the cache is populated.
 */
export function openSecondaryView(tab: BottomPanelTab): void {
  if (!WINDOWABLE_TYPES.includes(tab.type) || !isDesktopModeSync()) {
    usePanelStore.getState().openBottomTab(tab)
    return
  }
  switch (tab.type) {
    case 'logs':
      void openDesktopWindow({ kind: 'logs', id: tab.appName, title: tab.title })
      return
    case 'app-config': {
      // When the COG RMB menu picks a specific file, route the secondary
      // window at /window/editor/:name?file=<relPath> so WindowEditor can
      // open the per-file editor instead of the app YAML editor. Re-using a
      // distinct route + id pair keeps multiple file editors open at once
      // (Wails reuses windows by name, hence the per-file `id`).
      const filePath = tab.filePath
      if (filePath) {
        const file = encodeURIComponent(filePath)
        const id = `${tab.appName}::${filePath}`
        void openDesktopWindow({
          kind: 'editor',
          id,
          route: `/window/editor/${tab.appName}?file=${file}`,
          title: tab.title,
        })
        return
      }
      void openDesktopWindow({ kind: 'editor', id: tab.appName, title: tab.title })
      return
    }
    case 'daemon-logs':
      void openDesktopWindow({ kind: 'daemon-logs', title: tab.title })
      return
  }
}
