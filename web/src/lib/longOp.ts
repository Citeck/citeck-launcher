import { create } from 'zustand'

export type LongOpKind = 'snapshot.export' | 'snapshot.import' | 'snapshot.rename' | 'snapshot.delete' | 'snapshot.download'

export interface LongOpProgress {
  current: number
  total: number
  message?: string
}

export interface LongOp {
  kind: LongOpKind
  title: string
  progress?: LongOpProgress
  /** Epoch-ms when the op started. Drives the watchdog stall detection. */
  startedAt: number
  /** Epoch-ms of the last progress event received. */
  lastProgressAt: number
  /** True once the watchdog has flagged the op as stalled (SSE down + no
   *  progress for the configured windows). Surfaces a Dismiss affordance. */
  stalled: boolean
}

interface LongOpState {
  current: LongOp | null
  /** Bumped on every terminal snapshot event (complete/error). Surfaces to the
   *  SnapshotsDialog so it can reload its list once the backend finishes — an
   *  async export/import returns 202 before the file exists, so reloading right
   *  after the HTTP call shows a stale list. */
  completed: number
  start: (kind: LongOpKind, title: string) => void
  update: (progress: LongOpProgress) => void
  markProgress: () => void
  markStalled: () => void
  markCompleted: () => void
  end: () => void
}

export const useLongOpStore = create<LongOpState>((set) => ({
  current: null,
  completed: 0,
  start: (kind, title) => {
    const now = Date.now()
    set({ current: { kind, title, startedAt: now, lastProgressAt: now, stalled: false } })
  },
  update: (progress) =>
    set((s) =>
      s.current ? { current: { ...s.current, progress, lastProgressAt: Date.now(), stalled: false } } : s,
    ),
  markProgress: () =>
    set((s) => (s.current ? { current: { ...s.current, lastProgressAt: Date.now(), stalled: false } } : s)),
  markStalled: () =>
    set((s) => (s.current && !s.current.stalled ? { current: { ...s.current, stalled: true } } : s)),
  markCompleted: () => set((s) => ({ completed: s.completed + 1 })),
  end: () => set({ current: null }),
}))

export function startLongOp(kind: LongOpKind, title: string): void {
  useLongOpStore.getState().start(kind, title)
}

export function updateLongOp(progress: LongOpProgress): void {
  useLongOpStore.getState().update(progress)
}

export function endLongOp(): void {
  useLongOpStore.getState().end()
}
