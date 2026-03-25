import { create } from 'zustand'
import type { NamespaceDto, HealthDto } from './types'
import { getNamespace, getHealth } from './api'
import { connectEvents } from './websocket'

interface DashboardState {
  namespace: NamespaceDto | null
  health: HealthDto | null
  loading: boolean
  error: string | null
  ws: WebSocket | null

  fetchData: () => Promise<void>
  startEventStream: () => void
  stopEventStream: () => void
}

export const useDashboardStore = create<DashboardState>((set, get) => ({
  namespace: null,
  health: null,
  loading: true,
  error: null,
  ws: null,

  fetchData: async () => {
    set({ loading: true, error: null })
    try {
      const [namespace, health] = await Promise.all([getNamespace(), getHealth()])
      set({ namespace, health, loading: false })
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
    }
  },

  startEventStream: () => {
    const currentWs = get().ws
    if (currentWs) return // already connected

    const ws = connectEvents(
      () => {
        // On any event, refetch data
        get().fetchData()
      },
      () => {
        // On close, attempt reconnect only if ws is still the active one
        setTimeout(() => {
          if (get().ws === ws) {
            set({ ws: null })
            get().startEventStream()
          }
        }, 3000)
      },
    )
    set({ ws })
  },

  stopEventStream: () => {
    const { ws } = get()
    // Clear ref BEFORE close so the onClose callback sees ws !== get().ws
    set({ ws: null })
    if (ws) {
      ws.close()
    }
  },
}))
