import { create } from 'zustand'

export interface ErrorPayload {
  title?: string
  message: string
  details?: string
}

interface ErrorModalStore {
  current: ErrorPayload | null
  show: (payload: ErrorPayload) => void
  close: () => void
}

export const useErrorModalStore = create<ErrorModalStore>((set) => ({
  current: null,
  show: (payload) => set({ current: payload }),
  close: () => set({ current: null }),
}))

// Convenience helper used at call sites. Mirrors the toast() API style.
export function showError(payload: ErrorPayload | Error | string): void {
  if (typeof payload === 'string') {
    useErrorModalStore.getState().show({ message: payload })
    return
  }
  if (payload instanceof Error) {
    useErrorModalStore.getState().show({
      message: payload.message,
      details: payload.stack,
    })
    return
  }
  useErrorModalStore.getState().show(payload)
}
