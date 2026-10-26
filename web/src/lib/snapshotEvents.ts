import { ApiError, getSnapshotOperation } from './api'
import type { EventDto } from './types'
import { useLongOpStore } from './longOp'
import { t } from './i18n'
import { showError } from './errorModal'

/** One application path for live events, SSE replay and status reconciliation. */
export function applySnapshotEvent(event: EventDto): void {
  const ops = useLongOpStore.getState()
  // Polling may finish before SSE replays the operation's START frame.
  // Ignore the whole lifecycle, including progress, once its outcome is known.
  if (event.operationId && ops.completedOperationIds.includes(event.operationId)) return
  if (event.operationId && ops.current?.operationId && event.operationId !== ops.current.operationId) return
  if (event.type === 'snapshot_download' || event.type === 'snapshot_import') {
    const kind = event.type === 'snapshot_download' ? 'snapshot.download' : 'snapshot.import'
    if (!ops.current) ops.start(kind, t(`longOp.${kind}`))
    if (event.operationId) useLongOpStore.getState().bindOperation(event.operationId)
    useLongOpStore.getState().update({ current: 0, total: 0, phase: kind === 'snapshot.download' ? 'downloading' : 'importing' })
    return
  }
  if (event.type === 'snapshot_progress') {
    if (event.operationId) ops.bindOperation(event.operationId)
    useLongOpStore.getState().update({ receivedBytes: event.receivedBytes, totalBytes: event.totalBytes, phase: event.phase, current: event.current ?? 0, total: event.total ?? 0, message: event.after || undefined })
    return
  }
  if (event.type === 'snapshot_complete' || event.type === 'snapshot_error') {
    ops.end()
    ops.markCompleted(event.operationId)
    if (event.type === 'snapshot_error') showError({ title: t('snapshots.dialog.title'), message: event.after || t('errorModal.title') })
  }
}


/** Recover an exact accepted operation independently of SSE ring retention. */
export async function reconcileSnapshotOperation(): Promise<void> {
  const op = useLongOpStore.getState().current
  if (!op?.operationId) return
  try {
    const event = await getSnapshotOperation(op.operationId)
    if (useLongOpStore.getState().current?.token !== op.token) return
    // A status read is not fresh progress. Only an authoritative outcome can
    // end the overlay; repeating an old progress record must not reset idle time.
    if (event.type === 'snapshot_complete' || event.type === 'snapshot_error') applySnapshotEvent(event)
  } catch (error) {
    if (error instanceof ApiError && error.status === 404 && useLongOpStore.getState().current?.token === op.token) {
      useLongOpStore.getState().end()
      showError({ title: t('snapshots.dialog.title'), message: error.message })
    }
  }
}
