import { useState } from 'react'
import type { AppDto } from '../lib/types'
import { postAppStop, postAppStart, postAppRestart } from '../lib/api'
import { usePanelStore } from '../lib/panels'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'
import { StatusBadge } from './StatusBadge'
import { ConfirmModal } from './ConfirmModal'
import { Square, Play, RotateCw, FileText, Settings, Circle } from 'lucide-react'

interface AppTableProps {
  apps: AppDto[]
  highlightedApp?: string | null
}

type AppAction = { type: 'stop' | 'start' | 'restart'; appName: string } | null

const RUNNING = ['RUNNING']
const STOPPED = ['STOPPED', 'START_FAILED', 'PULL_FAILED', 'FAILED', 'STOPPING_FAILED']
const TRANSITIONAL = ['STARTING', 'PULLING', 'DEPS_WAITING', 'READY_TO_PULL', 'READY_TO_START', 'STOPPING']

const KIND_ORDER: Record<string, number> = { CITECK_CORE: 0, CITECK_CORE_EXTENSION: 1, CITECK_ADDITIONAL: 2, THIRD_PARTY: 3 }
const KIND_I18N: Record<string, string> = { CITECK_CORE: 'table.group.core', CITECK_CORE_EXTENSION: 'table.group.coreExt', CITECK_ADDITIONAL: 'table.group.additional', THIRD_PARTY: 'table.group.thirdParty' }

function groupByKind(apps: AppDto[]) {
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
      labelKey: KIND_I18N[kind] ?? kind,
      apps: apps.sort((a, b) => a.name.localeCompare(b.name)),
    }))
}

function tag(image: string) {
  const i = image.lastIndexOf(':')
  return i >= 0 ? image.substring(i + 1) : 'latest'
}

function portsShort(p?: string[]) {
  if (!p || !p.length) return ''
  const hostPorts = p.map((s) => { const a = s.split(':'); return a.length === 2 ? a[0] : s })
  if (hostPorts.length === 1) return hostPorts[0]
  return `${hostPorts[0]} ..`
}

export function AppTable({ apps, highlightedApp }: AppTableProps) {
  const [action, setAction] = useState<AppAction>(null)
  const [loading, setLoading] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const groups = groupByKind(apps)
  const { t } = useTranslation()

  async function handleConfirm() {
    if (!action) return
    setLoading(true); setActionError(null)
    try {
      if (action.type === 'stop') await postAppStop(action.appName)
      else if (action.type === 'start') await postAppStart(action.appName)
      else await postAppRestart(action.appName)
      toast(t('table.toast.success', { action: action.type.charAt(0).toUpperCase() + action.type.slice(1), name: action.appName }), 'success')
      setAction(null)
    } catch (err) { setActionError((err as Error).message) }
    finally { setLoading(false) }
  }

  const mc = action ? {
    stop: { title: t('table.confirm.stop.title', { name: action.appName }), msg: t('table.confirm.stop.message', { name: action.appName }), label: t('table.action.stop'), variant: 'danger' as const },
    start: { title: t('table.confirm.start.title', { name: action.appName }), msg: t('table.confirm.start.message', { name: action.appName }), label: t('table.action.start'), variant: 'primary' as const },
    restart: { title: t('table.confirm.restart.title', { name: action.appName }), msg: t('table.confirm.restart.message', { name: action.appName }), label: t('table.action.restart'), variant: 'primary' as const },
  }[action.type] : null

  return (
    <>
      <table className="w-full text-xs border-collapse">
        <thead>
          <tr className="text-left text-muted-foreground border-b border-border">
            <th className="py-1 pr-4 font-medium">{t('table.name')}</th>
            <th className="py-1 pr-4 font-medium">{t('table.status')}</th>
            <th className="py-1 pr-2 font-medium text-right w-16">{t('table.cpu')}</th>
            <th className="py-1 pr-4 font-medium text-right w-20">{t('table.mem')}</th>
            <th className="py-1 pr-4 font-medium w-20">{t('table.ports')}</th>
            <th className="py-1 pr-4 font-medium">{t('table.tag')}</th>
            <th className="py-1 font-medium text-right w-24">{t('table.actions')}</th>
          </tr>
        </thead>
        <tbody>
          {groups.map((g) => (
            <GroupRows key={g.kind} labelKey={g.labelKey} apps={g.apps} onAction={setAction} highlightedApp={highlightedApp} />
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

function GroupRows({ labelKey, apps, onAction, highlightedApp }: { labelKey: string; apps: AppDto[]; onAction: (a: AppAction) => void; highlightedApp?: string | null }) {
  const { openDrawer, openBottomTab } = usePanelStore()
  const { t } = useTranslation()

  return (
    <>
      <tr>
        <td colSpan={7} className="pt-4 pb-1 text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
          {t(labelKey)}
        </td>
      </tr>
      {apps.map((app) => {
        const isRun = RUNNING.includes(app.status)
        const isStop = STOPPED.includes(app.status)
        const isTransitional = TRANSITIONAL.includes(app.status)
        const isHighlighted = highlightedApp === app.name
        return (
          <tr key={app.name} className={`border-b border-border/20 ${isHighlighted ? 'bg-primary/8' : 'hover:bg-accent'}`}>
            <td className="py-[3px] pr-4 font-mono whitespace-nowrap">
              <button type="button" className="text-primary hover:underline cursor-pointer"
                onClick={() => openDrawer(app.name)}>
                {app.name}
              </button>
            </td>
            <td className="py-[3px] pr-4 whitespace-nowrap">
              <span className="inline-flex items-center gap-1.5">
                <StatusBadge status={app.status} />
                {app.statusText && <span className="text-muted-foreground text-[10px]">{app.statusText}</span>}
              </span>
            </td>
            <td className="py-[3px] pr-2 text-right font-mono text-muted-foreground">{app.cpu || ''}</td>
            <td className="py-[3px] pr-4 text-right font-mono text-muted-foreground">{app.memory ? app.memory.split(' / ')[0] : ''}</td>
            <td className="py-[3px] pr-4 font-mono text-muted-foreground whitespace-nowrap" title={app.ports?.join(', ')}>
              {portsShort(app.ports)}
            </td>
            <td className="py-[3px] pr-4 font-mono text-muted-foreground whitespace-nowrap cursor-pointer hover:text-foreground"
              title={t('table.copy', { image: app.image })}
              onClick={() => navigator.clipboard.writeText(app.image)}>
              {tag(app.image)}
            </td>
            <td className="py-[3px] text-right whitespace-nowrap">
              <div className="inline-flex items-center gap-0.5">
                {isRun && (
                  <>
                    <IconBtn icon={Square} title={t('table.action.stop')} color="hover:text-destructive" onClick={() => onAction({ type: 'stop', appName: app.name })} />
                    <IconBtn icon={RotateCw} title={t('table.action.restart')} onClick={() => onAction({ type: 'restart', appName: app.name })} />
                  </>
                )}
                {isStop && (
                  <IconBtn icon={Play} title={t('table.action.start')} color="hover:text-success" onClick={() => onAction({ type: 'start', appName: app.name })} />
                )}
                {isTransitional && (
                  <IconBtn icon={Square} title={t('table.action.stop')} color="hover:text-destructive" onClick={() => onAction({ type: 'stop', appName: app.name })} />
                )}
                <button type="button" className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted" title={t('logs.title', { name: app.name })}
                  onClick={() => openBottomTab({ id: `logs:${app.name}`, type: 'logs', title: t('logs.title', { name: app.name }), appName: app.name })}>
                  <FileText size={14} />
                </button>
                <button type="button" className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted relative"
                  title={app.edited ? (app.locked ? `${t('common.edit')} (${t('appConfig.lock.locked').toLowerCase()})` : t('common.edit')) : t('config.title')}
                  onClick={() => openBottomTab({ id: `app-config:${app.name}`, type: 'app-config', title: t('appConfig.tabTitle', { name: app.name }), appName: app.name })}>
                  <Settings size={14} />
                  {app.edited && <Circle size={6} className="absolute top-0.5 right-0.5 fill-blue-500 text-blue-500" />}
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
