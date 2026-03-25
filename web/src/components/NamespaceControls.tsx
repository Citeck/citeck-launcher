import { useState } from 'react'
import { postNamespaceStart, postNamespaceStop, postNamespaceReload } from '../lib/api'
import { useDashboardStore } from '../lib/store'
import { ConfirmModal } from './ConfirmModal'

interface NamespaceControlsProps {
  status: string
}

type Action = 'start' | 'stop' | 'reload' | null

const actionConfig = {
  start: {
    title: 'Start Namespace',
    message: 'Start all applications in this namespace?',
    confirmLabel: 'Start',
    confirmVariant: 'primary' as const,
    fn: postNamespaceStart,
  },
  stop: {
    title: 'Stop Namespace',
    message: 'Stop all running applications? This will shut down all containers.',
    confirmLabel: 'Stop',
    confirmVariant: 'danger' as const,
    fn: postNamespaceStop,
  },
  reload: {
    title: 'Reload Configuration',
    message: 'Reload namespace configuration? Running apps may be restarted.',
    confirmLabel: 'Reload',
    confirmVariant: 'primary' as const,
    fn: postNamespaceReload,
  },
}

export function NamespaceControls({ status }: NamespaceControlsProps) {
  const [pendingAction, setPendingAction] = useState<Action>(null)
  const [loading, setLoading] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const fetchData = useDashboardStore((s) => s.fetchData)

  const isStopped = status === 'STOPPED'
  const isRunning = status === 'RUNNING' || status === 'STALLED'

  async function handleConfirm() {
    if (!pendingAction) return
    setLoading(true)
    setActionError(null)
    try {
      await actionConfig[pendingAction].fn()
      setPendingAction(null)
      // Refetch after a short delay to allow state to propagate
      setTimeout(fetchData, 500)
    } catch (err) {
      setActionError((err as Error).message)
    } finally {
      setLoading(false)
    }
  }

  const config = pendingAction ? actionConfig[pendingAction] : null

  return (
    <>
      <div className="flex items-center gap-2">
        {(isStopped || status === 'STARTING') && (
          <button type="button" className="rounded border border-success/50 px-2 py-0.5 text-xs text-success hover:bg-success/10"
            onClick={() => setPendingAction('start')}>▶ Start</button>
        )}
        {(isRunning || status === 'STARTING') && (
          <button type="button" className="rounded border border-destructive/50 px-2 py-0.5 text-xs text-destructive hover:bg-destructive/10"
            onClick={() => setPendingAction('stop')}>⏹ Stop</button>
        )}
        {isRunning && (
          <button type="button" className="rounded border border-border px-2 py-0.5 text-xs text-muted-foreground hover:bg-muted"
            onClick={() => setPendingAction('reload')}>↻ Reload</button>
        )}
      </div>

      {config && (
        <ConfirmModal
          open={!!pendingAction}
          title={config.title}
          message={config.message}
          confirmLabel={config.confirmLabel}
          confirmVariant={config.confirmVariant}
          loading={loading}
          error={actionError}
          onConfirm={handleConfirm}
          onCancel={() => { setPendingAction(null); setActionError(null) }}
        />
      )}
    </>
  )
}
