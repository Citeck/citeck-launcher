import { useState } from 'react'
import { postNamespaceStart, postNamespaceStop, postNamespaceReload } from '../lib/api'
import { useDashboardStore } from '../lib/store'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'
import { showError } from '../lib/errorModal'
import { ConfirmModal } from './ConfirmModal'
import { ContextMenu, type ContextMenuItem } from './ContextMenu'
import { useContextMenu } from '../hooks/useContextMenu'
import { Play, Square, RefreshCw } from 'lucide-react'

interface NamespaceControlsProps {
  status: string
}

type Action = 'start' | 'stop' | 'reload' | 'forceStart' | null

const actionFns: Record<Exclude<Action, null>, () => Promise<unknown>> = {
  start: () => postNamespaceStart(false),
  forceStart: () => postNamespaceStart(true),
  stop: postNamespaceStop,
  reload: postNamespaceReload,
}

const actionVariants: Record<Exclude<Action, null>, 'primary' | 'danger'> = {
  start: 'primary',
  forceStart: 'primary',
  stop: 'danger',
  reload: 'primary',
}

export function NamespaceControls({ status }: NamespaceControlsProps) {
  const [pendingAction, setPendingAction] = useState<Action>(null)
  const [loading, setLoading] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const fetchData = useDashboardStore((s) => s.fetchData)
  const { t } = useTranslation()
  const { contextMenu, showContextMenu, hideContextMenu } = useContextMenu()

  const isStopped = status === 'STOPPED'
  const isRunning = status === 'RUNNING' || status === 'STALLED'

  async function handleConfirm() {
    if (!pendingAction) return
    setLoading(true)
    setActionError(null)
    try {
      await actionFns[pendingAction]()
      // Kotlin parity: surface a single user-facing label per action even
      // though forceStart maps to "start" semantically for the toast text.
      const toastAction = pendingAction === 'forceStart' ? 'start' : pendingAction
      toast(t('ns.toast.success', { action: toastAction }), 'success')
      setPendingAction(null)
      setTimeout(fetchData, 500)
    } catch (err) {
      const e = err as Error
      setActionError(e.message)
      showError({
        title: t(`ns.confirm.${pendingAction}.title`),
        message: e.message,
        details: e.stack,
      })
    } finally {
      setLoading(false)
    }
  }

  function startContextItems(): ContextMenuItem[] {
    return [
      { label: t('ns.forceStart'), onClick: () => setPendingAction('forceStart') },
    ]
  }

  const dialogKey: Exclude<Action, null> | null = pendingAction
  const configForAction = dialogKey ? {
    title: t(`ns.confirm.${dialogKey}.title`),
    message: t(`ns.confirm.${dialogKey}.message`),
    confirmLabel: t(`ns.${dialogKey}`),
    confirmVariant: actionVariants[dialogKey],
  } : null

  return (
    <>
      <div className="flex items-center gap-1.5">
        {isStopped && (
          <button type="button" className="flex items-center gap-1 rounded border border-success/40 px-2 py-1 text-xs text-success hover:bg-success/10"
            onClick={() => setPendingAction('start')}
            onContextMenu={(e) => { e.preventDefault(); showContextMenu(e, startContextItems()) }}
          ><Play size={12} /> {t('ns.start')}</button>
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

      {contextMenu && (
        <ContextMenu items={contextMenu.items} position={contextMenu.position} onClose={hideContextMenu} />
      )}

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
