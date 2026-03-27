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
  reconnectDelay: number
  lastSeq: number
  reconnectGen: number  // prevents race: two reconnects creating two EventSource streams

  fetchData: () => Promise<void>
  startEventStream: () => void
  stopEventStream: () => void
}

let fetchDebounceTimer: ReturnType<typeof setTimeout> | null = null

export const useDashboardStore = create<DashboardState>((set, get) => ({
  namespace: null,
  health: null,
  loading: true,
  error: null,
  stream: null,
  reconnectDelay: 1000,
  lastSeq: 0,
  reconnectGen: 0,

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
    // Close existing stream if any
    const prev = get().stream
    if (prev) prev.close()

    const gen = get().reconnectGen + 1
    set({ reconnectGen: gen })

    const stream = connectEvents(
      (event) => {
        // Detect sequence gap — fetch fresh state to catch up
        const { lastSeq } = get()
        if (lastSeq > 0 && event.seq > lastSeq + 1) {
          get().fetchData()
        }
        set({ lastSeq: event.seq })
        // Debounce: coalesce rapid event bursts into a single fetchData
        if (fetchDebounceTimer) clearTimeout(fetchDebounceTimer)
        fetchDebounceTimer = setTimeout(() => {
          fetchDebounceTimer = null
          get().fetchData()
        }, 100)
      },
      () => {
        // Only reconnect if this is still the current generation
        if (get().reconnectGen !== gen) return
        const delay = get().reconnectDelay
        const nextDelay = Math.min(delay * 2, 30000)
        set({ reconnectDelay: nextDelay, stream: null })
        setTimeout(() => {
          if (get().reconnectGen === gen) {
            get().startEventStream()
          }
        }, delay)
      },
      () => {
        set({ reconnectDelay: 1000 })
      },
    )
    set({ stream })
  },

  stopEventStream: () => {
    const { stream, reconnectGen } = get()
    set({ stream: null, reconnectDelay: 1000, lastSeq: 0, reconnectGen: reconnectGen + 1 })
    stream?.close()
  },
}))
