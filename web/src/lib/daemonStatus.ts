import { create } from 'zustand'
import { getDaemonStatus } from './api'
import type { DaemonStatusDto } from './types'

// Cached daemon status: server/desktop mode, active workspace, locale.
// Fetched once on first subscribe; subsequent reads come from cache.
// Without this, every component that needs to know the mode would issue
// its own GET /daemon/status (App, Welcome, Dashboard sidebar,
// WorkspaceSelector — 4+ duplicate requests on cold start).

interface DaemonStatusState {
  status: DaemonStatusDto | null
  loading: boolean
  error: string | null
  fetch: () => Promise<DaemonStatusDto | null>
  refresh: () => Promise<DaemonStatusDto | null>
}

let inFlight: Promise<DaemonStatusDto | null> | null = null

export const useDaemonStatusStore = create<DaemonStatusState>((set, get) => ({
  status: null,
  loading: false,
  error: null,
  fetch: async () => {
    const cached = get().status
    if (cached) return cached
    if (inFlight) return inFlight
    set({ loading: true, error: null })
    inFlight = getDaemonStatus()
      .then((s) => {
        set({ status: s, loading: false })
        return s
      })
      .catch((e) => {
        set({ error: String(e), loading: false })
        return null
      })
      .finally(() => {
        inFlight = null
      })
    return inFlight
  },
  refresh: async () => {
    inFlight = getDaemonStatus()
      .then((s) => {
        set({ status: s, loading: false, error: null })
        return s
      })
      .catch((e) => {
        set({ error: String(e), loading: false })
        return null
      })
      .finally(() => {
        inFlight = null
      })
    return inFlight
  },
}))

export function useIsDesktop(): boolean | null {
  const status = useDaemonStatusStore((s) => s.status)
  return status ? status.desktop : null
}

export function useActiveWorkspaceId(): string {
  const status = useDaemonStatusStore((s) => s.status)
  return status ? status.workspace : ''
}
