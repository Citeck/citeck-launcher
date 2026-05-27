import { create } from 'zustand'
import type { NamespaceDto, HealthDto } from './types'
import { getNamespace, getHealth } from './api'
import { connectEvents } from './websocket'
import { toast } from './toast'
import { t } from './i18n'
import { useLongOpStore } from './longOp'

interface EventStream {
  close: () => void
}

/** Transient per-app pull progress snapshot derived from `pull_progress` SSE events. */
export interface PullProgress {
  percent: number
  phase: string
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
  /** Per-app pull progress (live, transient — not from AppDto). Cleared when the
   * app leaves PULLING via `app_status`. Key = app name. */
  pullProgress: Record<string, PullProgress>
  /** Per-app registry host that needs credentials. Populated by `pull_auth_required`
   * SSE events; cleared on app_status transitions out of PULL_FAILED and after
   * the user dismisses/saves the RegistryCredentialsDialog. */
  pullAuthRequired: Record<string, string>

  fetchData: () => Promise<void>
  startEventStream: () => void
  stopEventStream: () => void
  clearPullAuthRequired: (appName: string) => void
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
  pullProgress: {},
  pullAuthRequired: {},

  clearPullAuthRequired: (appName: string) => {
    const cur = get().pullAuthRequired
    if (!(appName in cur)) return
    const next = { ...cur }
    delete next[appName]
    set({ pullAuthRequired: next })
  },

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

        // Fast path for `app_stats` — patch only the matching app's cpu/memory
        // in place to avoid the full namespace refetch (these fire every 5s
        // per running app). Other event types still trigger debounced fetch.
        if (event.type === 'app_stats' && event.appName) {
          const ns = get().namespace
          if (ns?.apps) {
            const apps = ns.apps.map((a) =>
              a.name === event.appName ? { ...a, cpu: event.before ?? a.cpu, memory: event.after ?? a.memory } : a,
            )
            set({ namespace: { ...ns, apps } })
          }
          return
        }

        // Snapshot lifecycle — drive the global LoadingOverlay so the user
        // cannot navigate away from a running export/import. Progress updates
        // come from `snapshot_progress`; the overlay clears on terminal
        // events (`snapshot_complete` / `snapshot_error`). Toasts are owned
        // by the dialog that initiated the op so they fire in the correct
        // localized phrasing.
        if (event.type === 'snapshot_progress') {
          useLongOpStore.getState().update({
            current: event.current ?? 0,
            total: event.total ?? 0,
            message: event.after || undefined,
          })
          return
        }
        if (event.type === 'snapshot_complete' || event.type === 'snapshot_error') {
          useLongOpStore.getState().end()
        }

        // Pull progress — store-side transient annotation, no AppDto change.
        // Backend throttles to ≤1/sec per app so we can update synchronously
        // without coalescing here.
        if (event.type === 'pull_progress' && event.appName) {
          const cur = get().pullProgress
          set({
            pullProgress: {
              ...cur,
              [event.appName]: { percent: event.percent ?? 0, phase: event.phase ?? '' },
            },
          })
          return
        }

        // Pull-auth-required — remember the host so the table can offer a
        // "Configure credentials" affordance. Backend emits once per
        // PULL_FAILED transition that classified as auth-error.
        if (event.type === 'pull_auth_required' && event.appName) {
          const host = event.after ?? ''
          if (host) {
            const cur = get().pullAuthRequired
            set({ pullAuthRequired: { ...cur, [event.appName]: host } })
          }
          return
        }

        // App status — clear transient annotations when the app leaves the
        // states they belong to (PULLING for progress, PULL_FAILED for
        // auth-required). The debounced fetch below still refreshes the
        // full namespace so the rest of the UI catches up.
        if (event.type === 'app_status' && event.appName) {
          const before = event.before
          const after = event.after
          // Leaving PULLING — drop progress so the bar disappears.
          if (before === 'PULLING' && after !== 'PULLING') {
            const cur = get().pullProgress
            if (event.appName in cur) {
              const next = { ...cur }
              delete next[event.appName]
              set({ pullProgress: next })
            }
          }
          // Leaving PULL_FAILED — drop the auth-required marker; if the new
          // pull also fails the backend will re-emit the event.
          if (before === 'PULL_FAILED' && after !== 'PULL_FAILED') {
            get().clearPullAuthRequired(event.appName)
          }
        }

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
    set({
      stream: null, reconnectDelay: 1000, lastSeq: 0, reconnectGen: reconnectGen + 1,
      pullProgress: {}, pullAuthRequired: {},
    })
    stream?.close()
  },
})})
