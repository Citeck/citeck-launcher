import { useState } from 'react'
import { postNamespaceStart, postNamespaceStop, postNamespaceReload } from '../lib/api'
import { useDashboardStore } from '../lib/store'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'
import { showError } from '../lib/errorModal'
import { ConfirmModal } from './ConfirmModal'
import { ContextMenu, type ContextMenuItem } from './ContextMenu'
import { useContextMenu } from '../hooks/useContextMenu'
import { Play, Square } from 'lucide-react'

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
  const isStarting = status === 'STARTING'
  // Kotlin parity: stop button is disabled when the namespace is already stopped.
  const stopEnabled = !isStopped
  // Kotlin parity: primary (Update&Start) is clickable while stopped or running.
  // While STARTING/STOPPING, the only safe operation is stop.
  const primaryEnabled = isStopped || isRunning
  // Kotlin used reload semantics when running, start when stopped.
  const primaryAction: Exclude<Action, null> = isRunning ? 'reload' : 'start'

  async function handleConfirm() {
    if (!pendingAction) return
    setLoading(true)
    setActionError(null)
    try {
      await actionFns[pendingAction]()
      // forceStart maps to "start" semantically for the toast label.
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

  function primaryContextItems(): ContextMenuItem[] {
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
      <div className="flex items-stretch h-7 rounded border border-border overflow-hidden">
        <button
          type="button"
          disabled={!primaryEnabled || isStarting}
          className={`flex items-center justify-center gap-1 px-2 text-xs border-r border-border ${
            primaryEnabled && !isStarting
              ? 'text-success hover:bg-success/10'
              : 'text-muted-foreground/40 cursor-not-allowed'
          }`}
          style={{ flex: 7 }}
          onClick={() => setPendingAction(primaryAction)}
          onContextMenu={(e) => { e.preventDefault(); if (primaryEnabled && !isStarting) showContextMenu(e, primaryContextItems()) }}
          title={t('ns.updateAndStart')}
        >
          <Play size={12} /> {t('ns.updateAndStart')}
        </button>
        <button
          type="button"
          disabled={!stopEnabled}
          className={`flex items-center justify-center gap-1 px-2 text-xs ${
            stopEnabled
              ? 'text-destructive hover:bg-destructive/10'
              : 'text-muted-foreground/40 cursor-not-allowed'
          }`}
          style={{ flex: 3 }}
          onClick={() => setPendingAction('stop')}
          title={t('ns.stop')}
        >
          <Square size={12} /> {t('ns.stop')}
        </button>
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
