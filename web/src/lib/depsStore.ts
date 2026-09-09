import { create } from 'zustand'
import { getDependencies } from './api'
import type { DependenciesDto, DependencyMigrationDto, DependencyUpgradeDto } from './types'

/** How many progress messages the dialog shows under the step list. */
const MAX_MESSAGES = 4

export interface DepsMigrationView {
  id: string
  step: string
  stepIndex: number
  stepCount: number
  /** The STEP's own sub-progress (0 = indeterminate), never an overall percentage. */
  percent: number
  message: string
  /** Step ids completed so far, in order — drives the checkmark list. */
  done: string[]
  /** Last few distinct progress messages, oldest first. */
  messages: string[]
  /**
   * "" = a migration, "rollback" = a rollback (Go: deps.ResultKindRollback).
   *
   * A namespace has ONE progress channel and the two operations share it, so
   * this is what decides which step list is rendered and which title. The SSE
   * events carry no discriminator at all — only the namespace DTO does — so it
   * is set by whoever KNOWS (the dialog, on the click that started it) and
   * corrected by `hydrate` for a run this client did not start.
   */
  kind: string
}

export interface DepsResultView {
  id: string
  success: boolean
  message: string
  at: number
  /** "" = a migration, "rollback" = a rollback — inherited from the run that
   *  produced it, because the complete/error events do not carry it either. */
  kind: string
}

/** Fields of a `deps_migration_progress` SSE event, in the daemon's mapping:
 *  appName = dependency id, phase = step id, current/total = step index/count. */
export interface DepsProgressEvent {
  appName: string
  phase: string
  current: number
  total: number
  percent: number
  after: string
}

interface DepsState {
  migration: DepsMigrationView | null
  result: DepsResultView | null
  /** Session-scoped banner dismissal, keyed by the upgrade set. */
  dismissedKey: string | null
  /**
   * Last payload of GET /namespace/dependencies. It lives here rather than in
   * the dialog because `lastResult` is NOT part of NamespaceDto, and the
   * banner has to show a pending rollback whether or not the dialog was ever
   * opened.
   */
  data: DependenciesDto | null
  /**
   * Why an interrupted migration's journal is still open with no migration
   * running — "" when there is nothing pending.
   *
   * Kept OUT of `data` because it has two writers reporting one daemon
   * condition in the same words: GET /namespace/dependencies (`refresh`) and
   * the ordinary namespace fetch (`NamespaceDto.dependencyRollbackPending`,
   * pushed in by the dashboard store). Whichever ran last is the freshest
   * answer, and both the banner and the dialog read this one field, so they
   * cannot disagree about a state that refuses every migration AND every start
   * of the namespace. Reading it off the dependencies payload alone is what
   * left the alarm waiting for a remount, a namespace switch, or the banner's
   * poll — which only starts once the alarm is already up.
   */
  rollbackPending: string
  onStart: (id: string, stepCount: number, kind?: string) => void
  onProgress: (e: DepsProgressEvent) => void
  onComplete: (id: string, message: string) => void
  onError: (id: string, message: string) => void
  hydrate: (dto: DependencyMigrationDto | null | undefined) => void
  setRollbackPending: (message: string) => void
  dismissBanner: (key: string) => void
  clearResult: () => void
  refresh: () => Promise<void>
}

/** Stable identity of "which upgrades are on offer" — the banner re-shows when it changes. */
export function upgradeSetKey(upgrades: DependencyUpgradeDto[] | undefined): string {
  return (upgrades ?? []).map((u) => `${u.id}:${u.to}`).join('|')
}

/**
 * The GET currently in flight, shared by every caller.
 *
 * The banner and the dialog are two views of ONE payload and they ask on the
 * same triggers — a verdict, a namespace switch, opening the dialog — so
 * without this the user paid two identical requests for every one of them.
 * A caller that joins an in-flight request accepts an answer that may have
 * been computed just before it asked; the route is answered from the daemon's
 * memory over a local socket, so that window is a round trip, against a
 * permanently doubled request rate.
 */
let inFlight: Promise<void> | null = null

function pushMessage(messages: string[], msg: string): string[] {
  if (!msg || messages[messages.length - 1] === msg) return messages
  return [...messages, msg].slice(-MAX_MESSAGES)
}

export const useDepsStore = create<DepsState>((set, get) => ({
  migration: null,
  result: null,
  dismissedKey: null,
  data: null,
  rollbackPending: '',

  onStart: (id, stepCount, kind = '') => {
    const cur = get().migration
    if (cur && cur.id === id) {
      // The daemon broadcasts `deps_migration_start` from the goroutine it
      // launches BEFORE writing the 202, so the dialog's optimistic start (on
      // the POST's answer) can land after real progress has been recorded.
      // Take the better step count and keep everything already seen.
      // The daemon's own `deps_migration_start` carries no kind, so an
      // already-known one is never downgraded to "" by it.
      set({
        migration: { ...cur, stepCount: Math.max(cur.stepCount, stepCount), kind: cur.kind || kind },
        result: null,
      })
      return
    }
    set({
      migration: { id, step: '', stepIndex: 0, stepCount, percent: 0, message: '', done: [], messages: [], kind },
      result: null,
    })
  },

  onProgress: (e) => {
    const cur = get().migration
    const done = cur ? [...cur.done] : []
    // A new step id means the previous one finished.
    if (cur && cur.step && cur.step !== e.phase && !done.includes(cur.step)) done.push(cur.step)
    set({
      migration: {
        id: e.appName,
        step: e.phase,
        stepIndex: e.current,
        stepCount: e.total,
        percent: e.percent,
        message: e.after,
        done,
        messages: pushMessage(cur?.messages ?? [], e.after),
        kind: cur?.kind ?? '',
      },
    })
  },

  // The verdict inherits the kind of the run it ends: the complete/error
  // events carry no discriminator, and "postgres 18.6 → 17.5 succeeded" reads
  // as a migration onto an older version unless the verdict says otherwise.
  onComplete: (id, message) => set({
    migration: null,
    result: { id, success: true, message, at: Date.now(), kind: get().migration?.kind ?? '' },
  }),
  onError: (id, message) => set({
    migration: null,
    result: { id, success: false, message, at: Date.now(), kind: get().migration?.kind ?? '' },
  }),

  hydrate: (dto) => {
    if (!dto) {
      if (get().migration) set({ migration: null })
      return
    }
    const cur = get().migration
    // Whatever is carried over belongs to THIS migration only: a different id
    // is a different run, and its checkmarks and messages would be somebody
    // else's history.
    const same = !!cur && cur.id === dto.id
    // The DTO is a snapshot from when the fetch was answered; SSE is live. If
    // both describe the same step, the events are the newer truth — adopting
    // the DTO would drag percent/message backwards on every refetch. The KIND
    // is the exception: it is constant for a whole run and the events never
    // carry it, so a run this client did not start learns it here.
    if (same && cur!.step === dto.step) {
      if (cur!.kind !== (dto.kind ?? '')) set({ migration: { ...cur!, kind: dto.kind ?? '' } })
      return
    }
    set({
      migration: {
        id: dto.id,
        step: dto.step,
        stepIndex: dto.stepIndex,
        stepCount: dto.stepCount,
        percent: dto.percent ?? 0,
        message: dto.message ?? '',
        done: same ? cur!.done : [],
        messages: same ? cur!.messages : [],
        kind: dto.kind ?? '',
      },
    })
  },

  setRollbackPending: (message) => {
    // Only on a real transition: this is written on every namespace fetch, and
    // the banner and the dialog subscribe to it.
    if (get().rollbackPending !== message) set({ rollbackPending: message })
  },

  dismissBanner: (key) => set({ dismissedKey: key }),
  clearResult: () => set({ result: null }),

  refresh: () => {
    if (inFlight) return inFlight
    // Deliberately no try/catch: the dialog surfaces the failure on the shared
    // error modal, the banner ignores it. Keeping the previous payload on a
    // failure matters — a transient error must not blank a rollback notice.
    // Both arms clear `inFlight` themselves rather than through `.finally`,
    // which would build a SECOND promise and reject it for whoever did not
    // attach a handler to it — an unhandled rejection instead of a refresh.
    inFlight = getDependencies().then(
      (data) => { inFlight = null; set({ data, rollbackPending: data.rollbackPending ?? '' }) },
      (e: unknown) => { inFlight = null; throw e },
    )
    return inFlight
  },
}))
