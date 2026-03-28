import { useState } from 'react'
import { postNamespaceStart, postNamespaceStop, postNamespaceReload } from '../lib/api'
import { useDashboardStore } from '../lib/store'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'
import { ConfirmModal } from './ConfirmModal'
import { Play, Square, RefreshCw } from 'lucide-react'

interface NamespaceControlsProps {
  status: string
}

type Action = 'start' | 'stop' | 'reload' | null

const actionFns = {
  start: postNamespaceStart,
  stop: postNamespaceStop,
  reload: postNamespaceReload,
}

const actionVariants = {
  start: 'primary' as const,
  stop: 'danger' as const,
  reload: 'primary' as const,
}

export function NamespaceControls({ status }: NamespaceControlsProps) {
  const [pendingAction, setPendingAction] = useState<Action>(null)
  const [loading, setLoading] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const fetchData = useDashboardStore((s) => s.fetchData)
  const { t } = useTranslation()

  const isStopped = status === 'STOPPED'
  const isRunning = status === 'RUNNING' || status === 'STALLED'

  async function handleConfirm() {
    if (!pendingAction) return
    setLoading(true)
    setActionError(null)
    try {
      await actionFns[pendingAction]()
      toast(t('ns.toast.success', { action: pendingAction }), 'success')
      setPendingAction(null)
      setTimeout(fetchData, 500)
    } catch (err) {
      toast((err as Error).message, 'error')
      setActionError((err as Error).message)
    } finally {
      setLoading(false)
    }
  }

  const configForAction = pendingAction ? {
    title: t(`ns.confirm.${pendingAction}.title`),
    message: t(`ns.confirm.${pendingAction}.message`),
    confirmLabel: t(`ns.${pendingAction}`),
    confirmVariant: actionVariants[pendingAction],
  } : null

  return (
    <>
      <div className="flex items-center gap-1.5">
        {isStopped && (
          <button type="button" className="flex items-center gap-1 rounded border border-success/40 px-2 py-1 text-xs text-success hover:bg-success/10"
            onClick={() => setPendingAction('start')}><Play size={12} /> {t('ns.start')}</button>
        )}
        {(isRunning || status === 'STARTING') && (
          <button type="button" className="flex items-center gap-1 rounded border border-destructive/40 px-2 py-1 text-xs text-destructive hover:bg-destructive/10"
            onClick={() => setPendingAction('stop')}><Square size={12} /> {t('ns.stop')}</button>
        )}
        {isRunning && (
          <button type="button" className="flex items-center gap-1 rounded border border-border px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
            onClick={() => setPendingAction('reload')}><RefreshCw size={12} /> {t('ns.reload')}</button>
        )}
      </div>

      {configForAction && (
        <ConfirmModal
          open={!!pendingAction}
          title={configForAction.title}
          message={configForAction.message}
          confirmLabel={configForAction.confirmLabel}
          confirmVariant={configForAction.confirmVariant}
          loading={loading}
          error={actionError}
          onConfirm={handleConfirm}
          onCancel={() => { setPendingAction(null); setActionError(null) }}
        />
      )}
    </>
  )
}
