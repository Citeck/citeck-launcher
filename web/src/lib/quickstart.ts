import { create } from 'zustand'

/**
 * Quick-start lifecycle state. Lives in a module-level store (not Welcome
 * component state) so the stepper survives route changes — the user is free
 * to navigate to the Dashboard mid-bootstrap and come back to /welcome, and
 * Welcome itself remounts when the index route flips from Welcome to
 * Dashboard once the namespace is created.
 *
 * Only the bootstrap *phase* lives here; all live progress (namespace +
 * app statuses, pull progress) comes from the existing dashboard SSE store.
 */
interface QuickStartState {
  /** A quick start is in progress (or finished and not yet dismissed). */
  active: boolean
  /** The createNamespace request (clone + bundle resolve) is in flight. */
  creating: boolean
  /** Fatal error from the create/start requests (SSE-level app failures are
   *  derived from app statuses instead and never set this). */
  error: string | null
  /** Machine-readable ApiError code of `error` (e.g. WS_REPO_SYNC_FAILED);
   *  '' when the failure wasn't an ApiError. */
  errorCode: string

  begin: () => void
  created: () => void
  fail: (message: string, code?: string) => void
  dismiss: () => void
}

export const useQuickStartStore = create<QuickStartState>((set) => ({
  active: false,
  creating: false,
  error: null,
  errorCode: '',

  begin: () => set({ active: true, creating: true, error: null, errorCode: '' }),
  created: () => set({ creating: false }),
  fail: (message: string, code = '') => set({ creating: false, error: message, errorCode: code }),
  dismiss: () => set({ active: false, creating: false, error: null, errorCode: '' }),
}))
