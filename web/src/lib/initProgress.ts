import type { AppDto } from './types'

export interface InitProgress {
  /** 1-based index of the init container currently running. */
  step: number
  /** Total init containers in the app's init phase. */
  total: number
  /** Short step name (init image basename); may be empty. */
  name: string
}

/**
 * Returns the app's init-container progress while the STARTING init phase is
 * active, or null otherwise. The daemon only populates initStep/initTotal
 * during the init phase of STARTING (cleared at phase end and on any status
 * change), and the status gate here protects against a stale SSE patch
 * landing after the app already moved on.
 */
export function initProgressOf(
  app: Pick<AppDto, 'status' | 'initStep' | 'initTotal' | 'initName'>,
): InitProgress | null {
  if (app.status !== 'STARTING') return null
  if (!app.initStep || !app.initTotal) return null
  return { step: app.initStep, total: app.initTotal, name: app.initName ?? '' }
}
