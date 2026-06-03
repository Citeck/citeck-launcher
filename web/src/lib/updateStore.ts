import { create } from 'zustand'
import { getUpdateStatus, checkUpdate } from './api'
import type { UpdateStatusDto } from './types'

interface UpdateStore {
  status: UpdateStatusDto | null
  /** Silent background refresh (no throw) — used on mount + periodic. */
  refresh: () => Promise<void>
  /** Explicit user-triggered check (forces a `latest` re-resolve). */
  check: () => Promise<void>
  setStatus: (s: UpdateStatusDto) => void
}

export const useUpdateStore = create<UpdateStore>((set) => ({
  status: null,
  refresh: async () => {
    try {
      set({ status: await getUpdateStatus() })
    } catch {
      /* desktop-only endpoint; 404 in server mode — stay silent */
    }
  },
  check: async () => {
    try {
      set({ status: await checkUpdate() })
    } catch {
      /* offline / server mode — Status().error carries detail when reachable */
    }
  },
  setStatus: (s) => set({ status: s }),
}))
