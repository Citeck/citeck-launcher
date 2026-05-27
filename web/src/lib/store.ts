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
  /** True while the SSE EventSource is open. Drives the longop watchdog. */
  sseConnected: boolean
  /** Epoch-ms of the last successful disconnect → reconnect cycle. */
  disconnectedAt: number | null
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

/** Watchdog thresholds — chosen to stay quiet on normal LAN/Wi-Fi blips
 *  (most reconnects land inside 5–10s) while still surfacing a stuck
 *  long-op within a reasonable upper bound on the reconnect side. */
const SSE_DISCONNECT_DISMISS_MS = 15_000
const LONGOP_PROGRESS_STALL_MS = 30_000
const WATCHDOG_TICK_MS = 5_000

export const useDashboardStore = create<DashboardState>((set, get) => {

let fetchDebounceTimer: ReturnType<typeof setTimeout> | null = null
let watchdogTimer: ReturnType<typeof setInterval> | null = null

const ensureWatchdog = () => {
  if (watchdogTimer !== null) return
  watchdogTimer = setInterval(() => {
    const longOp = useLongOpStore.getState().current
    if (!longOp || longOp.stalled) return
    const { sseConnected, disconnectedAt } = get()
    const now = Date.now()
    const disconnectedFor = !sseConnected && disconnectedAt !== null ? now - disconnectedAt : 0
    const sinceProgress = now - longOp.lastProgressAt
    if (disconnectedFor > SSE_DISCONNECT_DISMISS_MS && sinceProgress > LONGOP_PROGRESS_STALL_MS) {
      useLongOpStore.getState().markStalled()
    }
  }, WATCHDOG_TICK_MS)
}

const stopWatchdog = () => {
  if (watchdogTimer !== null) {
    clearInterval(watchdogTimer)
    watchdogTimer = null
  }
}

return ({
  namespace: null,
  health: null,
  loading: true,
  error: null,
  stream: null,
  reconnectDelay: 1000,
  lastSeq: 0,
  reconnectGen: 0,
  sseConnected: false,
  disconnectedAt: null,
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

    ensureWatchdog()

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
        // Any non-stats event counts as "activity" — keeps the watchdog
        // quiet during quiet periods of a long-running op (e.g. between
        // volumes during a large snapshot import).
        useLongOpStore.getState().markProgress()

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
        set({
          reconnectDelay: nextDelay,
          stream: null,
          sseConnected: false,
          disconnectedAt: get().disconnectedAt ?? Date.now(),
        })
        setTimeout(() => {
          if (get().reconnectGen === gen) {
            get().startEventStream()
          }
        }, delay)
      },
      () => {
        // onOpen — reconnect succeeded. Reset the backoff. Keep lastSeq so
        // gap detection still fires if the daemon's ring buffer wrapped
        // (the server emits an explicit `resync` event in that case and
        // also bumps Seq past lastSeq+1, both leading to fetchData()).
        set({ reconnectDelay: 1000, sseConnected: true, disconnectedAt: null })
      },
      () => {
        // Ring buffer wrapped past our lastSeq — daemon told us to resync.
        get().fetchData()
      },
      get().lastSeq,
    )
    set({ stream })
  },

  stopEventStream: () => {
    const { stream, reconnectGen } = get()
    if (fetchDebounceTimer) {
      clearTimeout(fetchDebounceTimer)
      fetchDebounceTimer = null
    }
    stopWatchdog()
    set({
      stream: null, reconnectDelay: 1000, lastSeq: 0, reconnectGen: reconnectGen + 1,
      sseConnected: false, disconnectedAt: null,
      pullProgress: {}, pullAuthRequired: {},
    })
    stream?.close()
  },
})})
