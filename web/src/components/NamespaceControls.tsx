import { postNamespaceStart, postNamespaceStop, postNamespaceReload } from '../lib/api'
import { useDashboardStore } from '../lib/store'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'
import { showError } from '../lib/errorModal'
import { ContextMenu, type ContextMenuItem } from './ContextMenu'
import { useContextMenu } from '../hooks/useContextMenu'
import { Play, Square } from 'lucide-react'

interface NamespaceControlsProps {
  status: string
}

type Action = 'start' | 'stop' | 'reload' | 'forceStart'

const actionFns: Record<Action, () => Promise<unknown>> = {
  start: () => postNamespaceStart(false),
  forceStart: () => postNamespaceStart(true),
  stop: postNamespaceStop,
  reload: postNamespaceReload,
}

export function NamespaceControls({ status }: NamespaceControlsProps) {
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
  const primaryAction: Action = isRunning ? 'reload' : 'start'

  // Fire start / stop / reload immediately. The ConfirmModal that used to
  // gate every click was double-bookkeeping for actions the user had already
  // explicitly clicked; errors go to the global error modal as before.
  async function run(a: Action) {
    try {
      await actionFns[a]()
      const toastAction = a === 'forceStart' ? 'start' : a
      toast(t('ns.toast.success', { action: toastAction }), 'success')
      setTimeout(fetchData, 500)
    } catch (err) {
      const e = err as Error
      showError({
        title: t(`ns.confirm.${a}.title`),
        message: e.message,
        details: e.stack,
      })
    }
  }

  function primaryContextItems(): ContextMenuItem[] {
    return [
      { label: t('ns.forceStart'), onClick: () => { void run('forceStart') } },
    ]
  }

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
          onClick={() => { void run(primaryAction) }}
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
          onClick={() => { void run('stop') }}
          title={t('ns.stop')}
        >
          <Square size={12} /> {t('ns.stop')}
        </button>
      </div>

      {contextMenu && (
        <ContextMenu items={contextMenu.items} position={contextMenu.position} onClose={hideContextMenu} />
      )}
    </>
  )
}
