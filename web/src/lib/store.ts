import { create } from 'zustand'
import type { NamespaceDto, HealthDto } from './types'
import { getNamespace, getHealth } from './api'
import { connectEvents } from './websocket'

interface EventStream {
  close: () => void
}

interface DashboardState {
  namespace: NamespaceDto | null
  health: HealthDto | null
  loading: boolean
  error: string | null
  stream: EventStream | null

  fetchData: () => Promise<void>
  startEventStream: () => void
  stopEventStream: () => void
}

export const useDashboardStore = create<DashboardState>((set, get) => ({
  namespace: null,
  health: null,
  loading: true,
  error: null,
  stream: null,

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
    if (get().stream) return

    const stream = connectEvents(
      () => { get().fetchData() },
      () => {
        setTimeout(() => {
          if (get().stream === stream) {
            set({ stream: null })
            get().startEventStream()
          }
        }, 3000)
      },
    )
    set({ stream })
  },

  stopEventStream: () => {
    const { stream } = get()
    set({ stream: null })
    stream?.close()
  },
}))
