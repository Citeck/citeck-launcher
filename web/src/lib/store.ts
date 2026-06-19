import { create } from 'zustand'
import type { NamespaceDto, HealthDto, EventDto } from './types'
import { getNamespace, getHealth } from './api'
import { connectEvents } from './websocket'
import { connectDesktopEvents, isWailsDesktop } from './desktopEvents'
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
  /** Latest low-disk condition from `disk_low` SSE events. Null when the disk
   * is OK (`disk_ok`) or the user dismissed the banner. The daemon emits on
   * state CHANGE only, so after a dismissal the banner reappears only on a
   * fresh trip (recovery followed by dropping below the threshold again). */
  diskLow: DiskLowInfo | null

  fetchData: () => Promise<void>
  startEventStream: () => void
  stopEventStream: () => void
  clearPullAuthRequired: (appName: string) => void
  dismissDiskLow: () => void
}

/** Payload of the daemon's `disk_low` SSE event (low-disk monitor). */
export interface DiskLowInfo {
  path: string
  freeBytes: number
  thresholdBytes: number
}

/** Watchdog thresholds — chosen to stay quiet on normal LAN/Wi-Fi blips
 *  (most reconnects land inside 5–10s) while still surfacing a stuck
 *  long-op within a reasonable upper bound on the reconnect side. */
const SSE_DISCONNECT_DISMISS_MS = 15_000
const LONGOP_PROGRESS_STALL_MS = 30_000
const WATCHDOG_TICK_MS = 5_000

/** Polling fallback for a non-delivering SSE stream — the Windows WebView2 asset
 *  server buffers the SSE response so EventSource never sees live frames
 *  (WebKitGTK/browsers stream fine and keep this dormant). Two phases:
 *   1. Until the stream PROVES itself live (first frame = data event OR `ping`),
 *      poll every POLL_FALLBACK_TICK_MS from the first tick — so a buffered
 *      transport refreshes within ~one tick, with NO long startup blackout.
 *   2. Once proven live, poll only if no frame has arrived within SSE_STALE_MS
 *      (catches a stream that goes silent mid-session). SSE_STALE_MS must exceed
 *      the daemon's 10s ping interval so a healthy idle stream never trips it. */
const SSE_STALE_MS = 14_000
const POLL_FALLBACK_TICK_MS = 3_000

export const useDashboardStore = create<DashboardState>((set, get) => {

let fetchDebounceTimer: ReturnType<typeof setTimeout> | null = null
let watchdogTimer: ReturnType<typeof setInterval> | null = null
// Epoch-ms of the last SSE frame (data event OR `ping`). Drives phase 2.
let lastSseFrameAt = 0
// True once the current SSE connection has delivered ≥1 frame — i.e. the
// transport actually streams. Reset on every (re)connect; stays false forever
// on a buffered transport (Windows WebView2), which keeps the poll in phase 1.
let sseLive = false
let pollTimer: ReturnType<typeof setInterval> | null = null
let pollInFlight = false

const ensurePollFallback = () => {
  if (pollTimer !== null) return
  pollTimer = setInterval(() => {
    if (pollInFlight) return
    // Nothing to refresh outside an active namespace (Welcome owns its own load).
    if (get().namespace === null) return
    // Phase 2: stream proven live AND a frame arrived recently — let SSE drive.
    if (sseLive && Date.now() - lastSseFrameAt < SSE_STALE_MS) return
    // Phase 1 (not yet proven live) or phase 2 gone stale → poll.
    pollInFlight = true
    void get().fetchData().finally(() => { pollInFlight = false })
  }, POLL_FALLBACK_TICK_MS)
}

const stopPollFallback = () => {
  if (pollTimer !== null) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

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
  diskLow: null,

  dismissDiskLow: () => set({ diskLow: null }),

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
      // The daemon explicitly reports no namespace (deactivated, deleted, or
      // never selected). Clear the stale namespace so the UI falls back to
      // Welcome and the workspace picker reappears — keeping the last namespace
      // pinned in the store hides the picker (TabBar keys off `namespace`) and
      // breaks the Welcome-at-root routing.
      if (msg.includes('no namespace configured') || msg.includes('NOT_CONFIGURED')) {
        set({ namespace: null, health: null, error: null, loading: false })
        return
      }
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
    // New connection: not proven live until its first frame arrives. Keeps the
    // poll fallback in phase 1 (poll every tick) until SSE proves it streams.
    sseLive = false

    const prev = get().stream
    if (prev) prev.close()

    ensureWatchdog()
    ensurePollFallback()

    const handleEvent = (event: EventDto) => {
        // Any frame (SSE or the desktop Wails bridge) proves the transport is
        // delivering — mark live and keep the polling fallback dormant.
        lastSseFrameAt = Date.now()
        sseLive = true
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

        // Low-disk monitor — the daemon emits `disk_low` once per trip and
        // `disk_ok` once on recovery (state change only). Drives the
        // dismissible Dashboard banner; no namespace refetch needed.
        if (event.type === 'disk_low') {
          set({
            diskLow: {
              path: event.path ?? '',
              freeBytes: event.freeBytes ?? 0,
              thresholdBytes: event.thresholdBytes ?? 0,
            },
          })
          return
        }
        if (event.type === 'disk_ok') {
          set({ diskLow: null })
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
          // Async export/import returned 202 before the file existed; signal the
          // SnapshotsDialog (if open) to reload now that the backend is done.
          useLongOpStore.getState().markCompleted()
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

        // Init-step progress — patch the matching app's init fields in place
        // (same fast-path idea as app_stats). The backend emits only when the
        // step index changes, so no coalescing is needed here. A phase-done
        // event arrives with current/total/after omitted → the fields clear
        // and the "init {step}/{total}" suffix disappears.
        if (event.type === 'app_init_step' && event.appName) {
          const ns = get().namespace
          if (ns?.apps) {
            const apps = ns.apps.map((a) =>
              a.name === event.appName
                ? { ...a, initStep: event.current, initTotal: event.total, initName: event.after || undefined }
                : a,
            )
            set({ namespace: { ...ns, apps } })
          }
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
    }

    if (isWailsDesktop()) {
      // Desktop: consume the daemon stream as native Wails events bridged by the
      // wrapper (cmd/citeck-desktop/eventbridge.go) — no in-page EventSource
      // (WebView2 buffers it) and no JS-side reconnect: the Go bridge owns the
      // daemon connection and re-emits a resync on every (re)connect. The HTTP
      // poll fallback still backs it up if the bridge ever stalls.
      const stream = connectDesktopEvents(
        handleEvent,
        () => { lastSseFrameAt = Date.now(); sseLive = true; get().fetchData() }, // resync
        () => { lastSseFrameAt = Date.now(); sseLive = true },                     // ping
      )
      set({ stream, sseConnected: true, disconnectedAt: null, reconnectDelay: 1000 })
      return
    }

    const stream = connectEvents(
      handleEvent,
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
        // NOTE: onOpen does NOT mark the stream live — a buffered WebView2
        // transport can fire onopen yet never deliver a frame. Liveness is
        // proven only by an actual frame (data event or `ping`), so the poll
        // fallback stays in phase 1 until then.
        set({ reconnectDelay: 1000, sseConnected: true, disconnectedAt: null })
      },
      () => {
        // Ring buffer wrapped past our lastSeq — daemon told us to resync.
        get().fetchData()
      },
      get().lastSeq,
      () => {
        // `ping` keepalive — transport is alive and delivering frames.
        lastSseFrameAt = Date.now()
        sseLive = true
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
    stopWatchdog()
    stopPollFallback()
    lastSseFrameAt = 0
    sseLive = false
    set({
      stream: null, reconnectDelay: 1000, lastSeq: 0, reconnectGen: reconnectGen + 1,
      sseConnected: false, disconnectedAt: null,
      pullProgress: {}, pullAuthRequired: {},
    })
    stream?.close()
  },
})})
