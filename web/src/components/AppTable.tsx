import { useState } from 'react'
import { Link } from 'react-router'
import type { AppDto } from '../lib/types'
import { postAppStop, postAppStart, postAppRestart } from '../lib/api'
import { useDashboardStore } from '../lib/store'
import { StatusBadge } from './StatusBadge'
import { ConfirmModal } from './ConfirmModal'

interface AppTableProps {
  apps: AppDto[]
}

type AppAction = { type: 'stop' | 'start' | 'restart'; appName: string } | null

const RUNNING_STATUSES = ['RUNNING']
const STOPPED_STATUSES = ['STOPPED', 'START_FAILED', 'PULL_FAILED', 'FAILED', 'STOPPING_FAILED']

export function AppTable({ apps }: AppTableProps) {
  const [pendingAction, setPendingAction] = useState<AppAction>(null)
  const [loading, setLoading] = useState(false)
  const fetchData = useDashboardStore((s) => s.fetchData)

  async function handleConfirm() {
    if (!pendingAction) return
    setLoading(true)
    try {
      switch (pendingAction.type) {
        case 'stop':
          await postAppStop(pendingAction.appName)
          break
        case 'start':
          await postAppStart(pendingAction.appName)
          break
        case 'restart':
          await postAppRestart(pendingAction.appName)
          break
      }
      setPendingAction(null)
      setTimeout(fetchData, 500)
    } catch (err) {
      console.error('App action failed:', err)
    } finally {
      setLoading(false)
    }
  }

  function getModalConfig() {
    if (!pendingAction) return null
    const { type, appName } = pendingAction
    switch (type) {
      case 'stop':
        return {
          title: `Stop ${appName}`,
          message: `Stop the ${appName} container?`,
          confirmLabel: 'Stop',
          confirmVariant: 'danger' as const,
        }
      case 'start':
        return {
          title: `Start ${appName}`,
          message: `Start the ${appName} container?`,
          confirmLabel: 'Start',
          confirmVariant: 'primary' as const,
        }
      case 'restart':
        return {
          title: `Restart ${appName}`,
          message: `Restart the ${appName} container? It will be briefly unavailable.`,
          confirmLabel: 'Restart',
          confirmVariant: 'primary' as const,
        }
    }
  }

  const modalConfig = getModalConfig()

  return (
    <>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border text-left text-muted-foreground">
              <th className="pb-3 pr-4 font-medium">APP</th>
              <th className="pb-3 pr-4 font-medium">STATUS</th>
              <th className="pb-3 pr-4 font-medium">IMAGE</th>
              <th className="pb-3 pr-4 font-medium text-right">CPU</th>
              <th className="pb-3 pr-4 font-medium text-right">MEMORY</th>
              <th className="pb-3 font-medium text-right">ACTIONS</th>
            </tr>
          </thead>
          <tbody>
            {apps.map((app) => {
              const isRunning = RUNNING_STATUSES.includes(app.status)
              const isStopped = STOPPED_STATUSES.includes(app.status)

              return (
                <tr key={app.name} className="border-b border-border/50 hover:bg-muted/30">
                  <td className="py-2.5 pr-4 font-mono text-sm">
                    <Link to={`/apps/${app.name}`} className="text-primary hover:underline">
                      {app.name}
                    </Link>
                  </td>
                  <td className="py-2.5 pr-4">
                    <StatusBadge status={app.status} />
                  </td>
                  <td className="py-2.5 pr-4 text-muted-foreground font-mono text-xs">
                    {app.image}
                  </td>
                  <td className="py-2.5 pr-4 text-right font-mono text-xs text-muted-foreground">
                    {app.cpu || '—'}
                  </td>
                  <td className="py-2.5 pr-4 text-right font-mono text-xs text-muted-foreground">
                    {app.memory || '—'}
                  </td>
                  <td className="py-2.5 text-right">
                    <div className="flex items-center justify-end gap-1">
                      {isRunning && (
                        <>
                          <button
                            type="button"
                            className="rounded px-2 py-1 text-xs text-destructive hover:bg-destructive/10"
                            onClick={() =>
                              setPendingAction({ type: 'stop', appName: app.name })
                            }
                          >
                            Stop
                          </button>
                          <button
                            type="button"
                            className="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted"
                            onClick={() =>
                              setPendingAction({ type: 'restart', appName: app.name })
                            }
                          >
                            Restart
                          </button>
                        </>
                      )}
                      {isStopped && (
                        <button
                          type="button"
                          className="rounded px-2 py-1 text-xs text-success hover:bg-success/10"
                          onClick={() =>
                            setPendingAction({ type: 'start', appName: app.name })
                          }
                        >
                          Start
                        </button>
                      )}
                      <Link
                        to={`/apps/${app.name}/logs`}
                        className="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted"
                      >
                        Logs
                      </Link>
                    </div>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      {modalConfig && (
        <ConfirmModal
          open={!!pendingAction}
          title={modalConfig.title}
          message={modalConfig.message}
          confirmLabel={modalConfig.confirmLabel}
          confirmVariant={modalConfig.confirmVariant}
          loading={loading}
          onConfirm={handleConfirm}
          onCancel={() => setPendingAction(null)}
        />
      )}
    </>
  )
}
