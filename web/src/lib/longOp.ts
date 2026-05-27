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
}

interface LongOpState {
  current: LongOp | null
  start: (kind: LongOpKind, title: string) => void
  update: (progress: LongOpProgress) => void
  end: () => void
}

export const useLongOpStore = create<LongOpState>((set) => ({
  current: null,
  start: (kind, title) => set({ current: { kind, title } }),
  update: (progress) =>
    set((s) => (s.current ? { current: { ...s.current, progress } } : s)),
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
