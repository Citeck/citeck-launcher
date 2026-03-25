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
  const fetchData = useDashboardStore((s) => s.fetchData)

  const isStopped = status === 'STOPPED'
  const isRunning = status === 'RUNNING' || status === 'STALLED'

  async function handleConfirm() {
    if (!pendingAction) return
    setLoading(true)
    try {
      await actionConfig[pendingAction].fn()
      setPendingAction(null)
      // Refetch after a short delay to allow state to propagate
      setTimeout(fetchData, 500)
    } catch (err) {
      console.error('Action failed:', err)
    } finally {
      setLoading(false)
    }
  }

  const config = pendingAction ? actionConfig[pendingAction] : null

  return (
    <>
      <div className="flex items-center gap-2">
        {(isStopped || status === 'STARTING') && (
          <button
            type="button"
            className="rounded-md bg-success px-3 py-1.5 text-xs font-medium text-white hover:bg-success/90"
            onClick={() => setPendingAction('start')}
          >
            Start
          </button>
        )}
        {(isRunning || status === 'STARTING') && (
          <button
            type="button"
            className="rounded-md bg-destructive px-3 py-1.5 text-xs font-medium text-white hover:bg-destructive/90"
            onClick={() => setPendingAction('stop')}
          >
            Stop
          </button>
        )}
        {isRunning && (
          <button
            type="button"
            className="rounded-md border border-border px-3 py-1.5 text-xs font-medium hover:bg-muted"
            onClick={() => setPendingAction('reload')}
          >
            Reload
          </button>
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
          onConfirm={handleConfirm}
          onCancel={() => setPendingAction(null)}
        />
      )}
    </>
  )
}
