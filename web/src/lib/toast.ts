import { create } from 'zustand'

export type ToastType = 'success' | 'error' | 'info'

// Optional action button rendered on the toast (e.g. "Undo"). Clicking it runs
// onClick and dismisses the toast. A button is reliable everywhere — unlike a
// keyboard shortcut, it isn't intercepted by the desktop webview.
export interface ToastAction {
  label: string
  onClick: () => void
}

export interface Toast {
  id: number
  message: string
  type: ToastType
  action?: ToastAction
}

let nextId = 1

// Bound the visible toast stack so a burst of errors can't push critical
// UI off-screen on small viewports. FIFO evict oldest when full.
const MAX_TOASTS = 5

interface ToastStore {
  toasts: Toast[]
  addToast: (message: string, type?: ToastType, action?: ToastAction) => void
  removeToast: (id: number) => void
}

export const useToastStore = create<ToastStore>((set) => ({
  toasts: [],
  addToast: (message, type = 'info', action) => {
    const id = nextId++
    set((s) => {
      const next = [...s.toasts, { id, message, type, action }]
      return { toasts: next.length > MAX_TOASTS ? next.slice(next.length - MAX_TOASTS) : next }
    })
    setTimeout(() => {
      set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) }))
    }, 5000)
  },
  removeToast: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
}))

export function toast(message: string, type: ToastType = 'info', action?: ToastAction) {
  useToastStore.getState().addToast(message, type, action)
}
