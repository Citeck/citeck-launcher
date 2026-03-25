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

const RUNNING = ['RUNNING']
const STOPPED = ['STOPPED', 'START_FAILED', 'PULL_FAILED', 'FAILED', 'STOPPING_FAILED']

const KIND_ORDER: Record<string, number> = { CITECK_CORE: 0, CITECK_CORE_EXTENSION: 1, CITECK_ADDITIONAL: 2, THIRD_PARTY: 3 }
const KIND_LABELS: Record<string, string> = { CITECK_CORE: 'Citeck Core', CITECK_CORE_EXTENSION: 'Citeck Core Extensions', CITECK_ADDITIONAL: 'Citeck Additional', THIRD_PARTY: 'Third Party' }

function groupByKind(apps: AppDto[]) {
  const groups = new Map<string, AppDto[]>()
  for (const app of apps) {
    const kind = app.kind || 'THIRD_PARTY'
    if (!groups.has(kind)) groups.set(kind, [])
    groups.get(kind)!.push(app)
  }
  return Array.from(groups.entries())
    .sort(([a], [b]) => (KIND_ORDER[a] ?? 99) - (KIND_ORDER[b] ?? 99))
    .map(([kind, apps]) => ({ kind, label: KIND_LABELS[kind] ?? kind, apps }))
}

function tag(image: string) {
  const i = image.lastIndexOf(':')
  return i >= 0 ? image.substring(i + 1) : 'latest'
}

function ports(p?: string[]) {
  if (!p || !p.length) return ''
  return p.map((s) => { const a = s.split(':'); return a.length === 2 ? a[0] : s }).join(', ')
}

export function AppTable({ apps }: AppTableProps) {
  const [action, setAction] = useState<AppAction>(null)
  const [loading, setLoading] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const fetchData = useDashboardStore((s) => s.fetchData)
  const groups = groupByKind(apps)

  async function handleConfirm() {
    if (!action) return
    setLoading(true); setActionError(null)
    try {
      if (action.type === 'stop') await postAppStop(action.appName)
      else if (action.type === 'start') await postAppStart(action.appName)
      else await postAppRestart(action.appName)
      setAction(null); setTimeout(fetchData, 500)
    } catch (err) { setActionError((err as Error).message) }
    finally { setLoading(false) }
  }

  const mc = action ? {
    stop: { title: `Stop ${action.appName}?`, msg: `Stop container ${action.appName}?`, label: 'Stop', variant: 'danger' as const },
    start: { title: `Start ${action.appName}?`, msg: `Start container ${action.appName}?`, label: 'Start', variant: 'primary' as const },
    restart: { title: `Restart ${action.appName}?`, msg: `Restart ${action.appName}?`, label: 'Restart', variant: 'primary' as const },
  }[action.type] : null

  return (
    <>
      {groups.map((g) => (
        <div key={g.kind} className="mb-1">
          <div className="text-xs font-bold py-1">{g.label}</div>
          <table className="w-full text-xs">
            <thead>
              <tr className="text-left text-muted-foreground border-b border-border">
                <th className="py-0.5 pr-6 font-medium">Name</th>
                <th className="py-0.5 pr-6 font-medium">Status</th>
                <th className="py-0.5 pr-6 font-medium">Ports</th>
                <th className="py-0.5 pr-6 font-medium">Tag</th>
                <th className="py-0.5 font-medium text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {g.apps.map((app) => {
                const isRun = RUNNING.includes(app.status)
                const isStop = STOPPED.includes(app.status)
                return (
                  <tr key={app.name} className="border-b border-border/20 hover:bg-muted/30">
                    <td className="py-[3px] pr-6 font-mono">
                      <Link to={`/apps/${app.name}`} className="text-primary hover:underline">{app.name}</Link>
                    </td>
                    <td className="py-[3px] pr-6"><StatusBadge status={app.status} /></td>
                    <td className="py-[3px] pr-6 font-mono text-muted-foreground whitespace-nowrap">{ports(app.ports)}</td>
                    <td className="py-[3px] pr-6 font-mono text-muted-foreground">{tag(app.image)}</td>
                    <td className="py-[3px] text-right whitespace-nowrap">
                      {isRun && <>
                        <AB c="text-muted-foreground" onClick={() => setAction({ type: 'stop', appName: app.name })}>⏹</AB>
                        <AB c="text-muted-foreground" onClick={() => setAction({ type: 'restart', appName: app.name })}>↻</AB>
                      </>}
                      {isStop && <AB c="text-success" onClick={() => setAction({ type: 'start', appName: app.name })}>▶</AB>}
                      <Link to={`/apps/${app.name}/logs`} className="inline-block px-1 text-muted-foreground hover:text-foreground" title="Logs">📋</Link>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      ))}
      {mc && (
        <ConfirmModal open={!!action} title={mc.title} message={mc.msg}
          confirmLabel={mc.label} confirmVariant={mc.variant}
          loading={loading} error={actionError}
          onConfirm={handleConfirm} onCancel={() => { setAction(null); setActionError(null) }}
        />
      )}
    </>
  )
}

function AB({ children, c, onClick }: { children: React.ReactNode; c: string; onClick: () => void }) {
  return <button type="button" className={`px-1 ${c} hover:text-foreground`} onClick={onClick} title={String(children)}>{children}</button>
}
