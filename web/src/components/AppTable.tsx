import { useState } from 'react'
import { useNavigate } from 'react-router'
import type { AppDto } from '../lib/types'
import { postAppStop, postAppStart, postAppRestart } from '../lib/api'
import { useDashboardStore } from '../lib/store'
import { useTabsStore } from '../lib/tabs'
import { StatusBadge } from './StatusBadge'
import { ConfirmModal } from './ConfirmModal'
import { Square, Play, RotateCw, FileText, Settings } from 'lucide-react'

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
      <table className="w-full text-xs border-collapse">
        <thead>
          <tr className="text-left text-muted-foreground border-b border-border">
            <th className="py-1 pr-4 font-medium">Name</th>
            <th className="py-1 pr-4 font-medium">Status</th>
            <th className="py-1 pr-4 font-medium">Ports</th>
            <th className="py-1 pr-4 font-medium">Tag</th>
            <th className="py-1 font-medium text-right w-24">Actions</th>
          </tr>
        </thead>
        <tbody>
          {groups.map((g) => (
            <GroupRows key={g.kind} label={g.label} count={g.apps.length} apps={g.apps} onAction={setAction} />
          ))}
        </tbody>
      </table>

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

function GroupRows({ label, apps, onAction }: { label: string; apps: AppDto[]; onAction: (a: AppAction) => void }) {
  const navigate = useNavigate()
  const openTab = useTabsStore((s) => s.openTab)

  function openInTab(id: string, title: string, path: string) {
    openTab({ id, title, path })
    navigate(path)
  }

  return (
    <>
      <tr>
        <td colSpan={5} className="pt-3 pb-1 text-xs font-bold text-foreground">
          {label}
        </td>
      </tr>
      {apps.map((app) => {
        const isRun = RUNNING.includes(app.status)
        const isStop = STOPPED.includes(app.status)
        return (
          <tr key={app.name} className="border-b border-border/20 hover:bg-muted/30">
            <td className="py-[3px] pr-4 font-mono">
              <a href={`/apps/${app.name}`} className="text-primary hover:underline cursor-pointer"
                onClick={(e) => { e.preventDefault(); openInTab(`app-${app.name}`, app.name, `/apps/${app.name}`) }}>
                {app.name}
              </a>
            </td>
            <td className="py-[3px] pr-4"><StatusBadge status={app.status} /></td>
            <td className="py-[3px] pr-4 font-mono text-muted-foreground whitespace-nowrap">{ports(app.ports)}</td>
            <td className="py-[3px] pr-4 font-mono text-muted-foreground">{tag(app.image)}</td>
            <td className="py-[3px] text-right whitespace-nowrap">
              <div className="inline-flex items-center gap-0.5">
                {isRun && (
                  <>
                    <IconBtn icon={Square} title="Stop" color="hover:text-destructive" onClick={() => onAction({ type: 'stop', appName: app.name })} />
                    <IconBtn icon={RotateCw} title="Restart" onClick={() => onAction({ type: 'restart', appName: app.name })} />
                  </>
                )}
                {isStop && (
                  <IconBtn icon={Play} title="Start" color="hover:text-success" onClick={() => onAction({ type: 'start', appName: app.name })} />
                )}
                <button type="button" className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted" title="Logs"
                  onClick={() => openInTab(`logs-${app.name}`, `Logs: ${app.name}`, `/apps/${app.name}/logs`)}>
                  <FileText size={14} />
                </button>
                <button type="button" className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted" title="Details"
                  onClick={() => openInTab(`app-${app.name}`, app.name, `/apps/${app.name}`)}>
                  <Settings size={14} />
                </button>
              </div>
            </td>
          </tr>
        )
      })}
    </>
  )
}

function IconBtn({ icon: Icon, title, color, onClick }: { icon: React.ElementType; title: string; color?: string; onClick: () => void }) {
  return (
    <button
      type="button"
      className={`p-1 rounded text-muted-foreground ${color ?? 'hover:text-foreground'} hover:bg-muted`}
      onClick={onClick}
      title={title}
    >
      <Icon size={14} />
    </button>
  )
}
