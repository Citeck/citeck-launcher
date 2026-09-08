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
}

export interface DepsResultView {
  id: string
  success: boolean
  message: string
  at: number
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
   * the dialog because `rollbackPending` and `lastResult` are NOT part of
   * NamespaceDto, and the banner has to show a pending rollback whether or not
   * the dialog was ever opened.
   */
  data: DependenciesDto | null
  onStart: (id: string, stepCount: number) => void
  onProgress: (e: DepsProgressEvent) => void
  onComplete: (id: string, message: string) => void
  onError: (id: string, message: string) => void
  hydrate: (dto: DependencyMigrationDto | null | undefined) => void
  dismissBanner: (key: string) => void
  clearResult: () => void
  refresh: () => Promise<void>
}

/** Stable identity of "which upgrades are on offer" — the banner re-shows when it changes. */
export function upgradeSetKey(upgrades: DependencyUpgradeDto[] | undefined): string {
  return (upgrades ?? []).map((u) => `${u.id}:${u.to}`).join('|')
}

function pushMessage(messages: string[], msg: string): string[] {
  if (!msg || messages[messages.length - 1] === msg) return messages
  return [...messages, msg].slice(-MAX_MESSAGES)
}

export const useDepsStore = create<DepsState>((set, get) => ({
  migration: null,
  result: null,
  dismissedKey: null,
  data: null,

  onStart: (id, stepCount) => {
    const cur = get().migration
    if (cur && cur.id === id) {
      // The daemon broadcasts `deps_migration_start` from the goroutine it
      // launches BEFORE writing the 202, so the dialog's optimistic start (on
      // the POST's answer) can land after real progress has been recorded.
      // Take the better step count and keep everything already seen.
      set({ migration: { ...cur, stepCount: Math.max(cur.stepCount, stepCount) }, result: null })
      return
    }
    set({
      migration: { id, step: '', stepIndex: 0, stepCount, percent: 0, message: '', done: [], messages: [] },
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
      },
    })
  },

  onComplete: (id, message) => set({ migration: null, result: { id, success: true, message, at: Date.now() } }),
  onError: (id, message) => set({ migration: null, result: { id, success: false, message, at: Date.now() } }),

  hydrate: (dto) => {
    if (!dto) {
      if (get().migration) set({ migration: null })
      return
    }
    const cur = get().migration
    // The DTO is a snapshot from when the fetch was answered; SSE is live. If
    // both describe the same step, the events are the newer truth — adopting
    // the DTO would drag percent/message backwards on every refetch.
    if (cur && cur.id === dto.id && cur.step === dto.step) return
    set({
      migration: {
        id: dto.id,
        step: dto.step,
        stepIndex: dto.stepIndex,
        stepCount: dto.stepCount,
        percent: dto.percent ?? 0,
        message: dto.message ?? '',
        done: cur?.done ?? [],
        messages: cur?.messages ?? [],
      },
    })
  },

  dismissBanner: (key) => set({ dismissedKey: key }),
  clearResult: () => set({ result: null }),

  refresh: async () => {
    // Deliberately no try/catch: the dialog surfaces the failure on the shared
    // error modal, the banner ignores it. Keeping the previous payload on a
    // failure matters — a transient error must not blank a rollback notice.
    set({ data: await getDependencies() })
  },
}))
