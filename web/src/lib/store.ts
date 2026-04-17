import { create } from 'zustand'
import type { NamespaceDto, HealthDto } from './types'
import { getNamespace, getHealth } from './api'
import { connectEvents } from './websocket'
import { toast } from './toast'
import { t } from './i18n'

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

export const useDashboardStore = create<DashboardState>((set, get) => {

let fetchDebounceTimer: ReturnType<typeof setTimeout> | null = null

return ({
  namespace: null,
  health: null,
  loading: true,
  error: null,
  stream: null,
  reconnectDelay: 1000,
  lastSeq: 0,
  reconnectGen: 0,

  fetchData: async () => {
    const isInitial = get().namespace === null
    if (isInitial) set({ loading: true })
    set({ error: null })
    try {
      const [namespace, health] = await Promise.all([getNamespace(), getHealth()])
      set({ namespace, health, loading: false })
    } catch (err) {
      const msg = (err as Error).message
      // Daemon still starting — retry silently instead of showing error
      if (msg.includes('Service Unavailable') || msg.includes('503') || msg.includes('DAEMON_STARTING') || msg.includes('Failed to fetch')) {
        if (isInitial) {
          setTimeout(() => get().fetchData(), 1000)
          return
        }
      }
      set({ error: msg, loading: false })
    }
  },

  startEventStream: () => {
    // Bump generation BEFORE closing the old stream so any in-flight onClose
    // callback from the previous stream (captured the old gen) sees the
    // updated counter and skips its reconnect branch. Otherwise a rapid
    // restart could double-schedule reconnects.
    const gen = get().reconnectGen + 1
    set({ reconnectGen: gen })

    const prev = get().stream
    if (prev) prev.close()

    const stream = connectEvents(
      (event) => {
        // Detect sequence gap — fetch fresh state to catch up
        const { lastSeq } = get()
        if (lastSeq > 0 && event.seq > lastSeq + 1) {
          toast(t('store.connectionRestored'), 'info')
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
        // onOpen — reconnect succeeded. Reset the backoff. Also clear lastSeq
        // so the "sequence gap" path inside onEvent doesn't fire the
        // "connection restored" toast on the very first event of a brand-new
        // stream (gap detection is meaningful only once we've seen at least
        // one seq from the current stream).
        set({ reconnectDelay: 1000, lastSeq: 0 })
      },
    )
    set({ stream })
  },

  stopEventStream: () => {
    const { stream, reconnectGen } = get()
    if (fetchDebounceTimer) {
      clearTimeout(fetchDebounceTimer)
      fetchDebounceTimer = null
    }
    set({ stream: null, reconnectDelay: 1000, lastSeq: 0, reconnectGen: reconnectGen + 1 })
    stream?.close()
  },
})})
