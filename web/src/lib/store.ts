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
    const ws = connectEvents(
      () => {
        // On any event, refetch data
        get().fetchData()
      },
      () => {
        // On close, attempt reconnect after 3s
        setTimeout(() => {
          if (get().ws) get().startEventStream()
        }, 3000)
      },
    )
    set({ ws })
  },

  stopEventStream: () => {
    const { ws } = get()
    if (ws) {
      ws.close()
      set({ ws: null })
    }
  },
}))
