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

const KIND_ORDER: Record<string, number> = {
  CITECK_CORE: 0,
  CITECK_CORE_EXTENSION: 1,
  CITECK_ADDITIONAL: 2,
  THIRD_PARTY: 3,
}

const KIND_LABELS: Record<string, string> = {
  CITECK_CORE: 'Core',
  CITECK_CORE_EXTENSION: 'Extensions',
  CITECK_ADDITIONAL: 'Additional',
  THIRD_PARTY: 'Infrastructure',
}

function groupByKind(apps: AppDto[]): { kind: string; label: string; apps: AppDto[] }[] {
  const groups = new Map<string, AppDto[]>()
  for (const app of apps) {
    const kind = app.kind || 'THIRD_PARTY'
    if (!groups.has(kind)) groups.set(kind, [])
    groups.get(kind)!.push(app)
  }
  return Array.from(groups.entries())
    .sort(([a], [b]) => (KIND_ORDER[a] ?? 99) - (KIND_ORDER[b] ?? 99))
    .map(([kind, apps]) => ({
      kind,
      label: KIND_LABELS[kind] ?? kind,
      apps,
    }))
}

function extractTag(image: string): string {
  const idx = image.lastIndexOf(':')
  return idx >= 0 ? image.substring(idx + 1) : 'latest'
}

function formatPorts(ports?: string[]): string {
  if (!ports || ports.length === 0) return '—'
  return ports
    .map((p) => {
      // "8080:80" → "8080→80"
      const parts = p.split(':')
      return parts.length === 2 ? `${parts[0]}→${parts[1]}` : p
    })
    .join(', ')
}

export function AppTable({ apps }: AppTableProps) {
  const [pendingAction, setPendingAction] = useState<AppAction>(null)
  const [loading, setLoading] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const fetchData = useDashboardStore((s) => s.fetchData)

  const groups = groupByKind(apps)

  async function handleConfirm() {
    if (!pendingAction) return
    setLoading(true)
    setActionError(null)
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
      setActionError((err as Error).message)
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
              <th className="pb-3 pr-4 font-medium">TAG</th>
              <th className="pb-3 pr-4 font-medium">PORTS</th>
              <th className="pb-3 pr-4 font-medium text-right">CPU</th>
              <th className="pb-3 pr-4 font-medium text-right">MEMORY</th>
              <th className="pb-3 font-medium text-right">ACTIONS</th>
            </tr>
          </thead>
          <tbody>
            {groups.map((group) => (
              <GroupRows
                key={group.kind}
                label={group.label}
                apps={group.apps}
                onAction={setPendingAction}
              />
            ))}
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
          error={actionError}
          onConfirm={handleConfirm}
          onCancel={() => { setPendingAction(null); setActionError(null) }}
        />
      )}
    </>
  )
}

function GroupRows({
  label,
  apps,
  onAction,
}: {
  label: string
  apps: AppDto[]
  onAction: (action: AppAction) => void
}) {
  return (
    <>
      <tr>
        <td
          colSpan={7}
          className="pt-4 pb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground"
        >
          {label}
          <span className="ml-2 font-normal">({apps.length})</span>
        </td>
      </tr>
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
            <td className="py-2.5 pr-4 font-mono text-xs text-muted-foreground">
              {extractTag(app.image)}
            </td>
            <td className="py-2.5 pr-4 font-mono text-xs text-muted-foreground">
              {formatPorts(app.ports)}
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
                      onClick={() => onAction({ type: 'stop', appName: app.name })}
                    >
                      Stop
                    </button>
                    <button
                      type="button"
                      className="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted"
                      onClick={() => onAction({ type: 'restart', appName: app.name })}
                    >
                      Restart
                    </button>
                  </>
                )}
                {isStopped && (
                  <button
                    type="button"
                    className="rounded px-2 py-1 text-xs text-success hover:bg-success/10"
                    onClick={() => onAction({ type: 'start', appName: app.name })}
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
    </>
  )
}
