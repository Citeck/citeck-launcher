import { useState } from 'react'
import type { AppDto } from '../lib/types'
import { postAppStop, postAppStart, postAppRestart, getAppFiles, postAppsRetryPullFailed } from '../lib/api'
import { usePanelStore } from '../lib/panels'
import { openSecondaryView } from '../lib/desktop'
import { ContextMenu, type ContextMenuItem } from './ContextMenu'
import { useContextMenu } from '../hooks/useContextMenu'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'
import { useDashboardStore } from '../lib/store'
import { RegistryCredentialsDialog } from './RegistryCredentialsDialog'
import { KeyRound } from 'lucide-react'
import { isEditableFile } from '../lib/files'
import { StatusBadge } from './StatusBadge'
import { StatsCell } from './StatsCell'
import { ConfirmModal } from './ConfirmModal'
import { Square, Play, RotateCw, FileText, Settings, Circle, Lock } from 'lucide-react'

// "2.3%" / "0%" → numeric percent. Empty/unparseable yields 0.
function parseCpuPercent(s?: string): number {
  if (!s) return 0
  const m = s.match(/-?\d+(?:\.\d+)?/)
  return m ? parseFloat(m[0]) : 0
}

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
      <table className="w-full text-xs border-collapse table-fixed">
        {/* Column widths follow Kotlin AppTableColumns.kt: NAME / STATUS are
            proportional (NAME wider), CPU / MEM / PORTS / TAG / ACTIONS are
            fixed in dp. table-fixed prevents a wide status-text from pushing
            the whole row sideways — long content truncates inside its cell. */}
        <colgroup>
          <col style={{ width: '28%' }} />
          <col style={{ width: 'auto' }} />
          <col style={{ width: '70px' }} />
          <col style={{ width: '90px' }} />
          <col style={{ width: '80px' }} />
          <col style={{ width: '120px' }} />
          <col style={{ width: '104px' }} />
        </colgroup>
        <thead>
          <tr className="text-left text-muted-foreground border-b border-border">
            <th className="py-1 pr-4 font-medium">{t('table.name')}</th>
            <th className="py-1 pr-4 font-medium">{t('table.status')}</th>
            <th className="py-1 pr-2 font-medium text-right">{t('table.cpu')}</th>
            <th className="py-1 pr-4 font-medium text-right">{t('table.mem')}</th>
            <th className="py-1 pr-4 font-medium">{t('table.ports')}</th>
            <th className="py-1 pr-4 font-medium">{t('table.tag')}</th>
            <th className="py-1 font-medium text-right">{t('table.actions')}</th>
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
  const { openDrawer } = usePanelStore()
  const { t } = useTranslation()
  const { contextMenu, showContextMenu, hideContextMenu } = useContextMenu()
  const pullProgress = useDashboardStore((s) => s.pullProgress)
  const pullAuthRequired = useDashboardStore((s) => s.pullAuthRequired)
  const clearPullAuthRequired = useDashboardStore((s) => s.clearPullAuthRequired)
  // Single dialog instance per group; opened with the active app's host so the
  // creds form is pre-filled. Closing also clears the per-app auth marker.
  const [credsFor, setCredsFor] = useState<{ app: string; host: string } | null>(null)

  function handleCredsClose() {
    setCredsFor(null)
  }

  async function handleCredsSaved() {
    const target = credsFor
    if (target) clearPullAuthRequired(target.app)
    try {
      await postAppsRetryPullFailed()
      toast(t('table.pullAuthRetry.success'), 'success')
    } catch (e) {
      toast((e as Error).message, 'error')
    }
  }

  async function buildCogMenu(appName: string): Promise<ContextMenuItem[]> {
    try {
      const files = await getAppFiles(appName)
      const items: ContextMenuItem[] = files
        .filter((f) => isEditableFile(f.path))
        .map((f) => ({
          label: f.path,
          // Kotlin v1.3.8 rendered a 5dp blue vertical bar before edited
          // entries in the COG RMB menu — same visual marker the inline list
          // already shows.
          decoration: f.edited ? (
            <span
              className="inline-block w-0.5 h-3 bg-blue-500 mr-1.5 align-middle shrink-0"
              title={t('appConfig.fileEdited.badge')}
            />
          ) : undefined,
          onClick: () => openSecondaryView({
            id: `editor:${appName}:${f.path}`,
            type: 'app-config',
            title: t('appConfig.tabTitle', { name: `${appName} — ${f.path}` }),
            appName,
            filePath: f.path,
          }),
        }))
      if (items.length === 0) {
        items.push({ label: t('table.noEditableFiles'), onClick: () => {}, variant: 'danger' })
      }
      return items
    } catch (e) {
      return [{ label: (e as Error).message, onClick: () => {}, variant: 'danger' }]
    }
  }

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
            <td className="py-[3px] pr-4 overflow-hidden">
              <span className="flex items-center gap-1.5 min-w-0">
                <StatusBadge status={app.status} />
                {(app.restartCount ?? 0) > 0 && (
                  <span className="ml-1 inline-flex shrink-0 items-center rounded bg-destructive/10 px-1 py-0 text-[10px] font-medium text-destructive leading-4"
                    title={t('table.restartCount')}>
                    {'\u21bb'}{app.restartCount}
                  </span>
                )}
                {app.status === 'PULLING' && pullProgress[app.name] ? (
                  <PullProgressBar
                    percent={pullProgress[app.name].percent}
                    phase={pullProgress[app.name].phase}
                  />
                ) : (
                  app.statusText && (
                    <span
                      className="text-muted-foreground text-[10px] truncate min-w-0"
                      title={app.statusText}
                    >
                      {app.statusText}
                    </span>
                  )
                )}
                {app.status === 'PULL_FAILED' && pullAuthRequired[app.name] && (
                  <button
                    type="button"
                    className="inline-flex items-center gap-1 rounded border border-warning/40 bg-warning/10 px-1.5 py-0 text-[10px] font-medium text-warning hover:bg-warning/20 leading-4"
                    title={t('table.pullAuthRequired.tooltip')}
                    onClick={(e) => {
                      e.stopPropagation()
                      setCredsFor({ app: app.name, host: pullAuthRequired[app.name] })
                    }}
                  >
                    <KeyRound size={10} />
                    {t('table.pullAuthRequired.label')}
                  </button>
                )}
              </span>
            </td>
            <td className="py-[3px] pr-2 text-right">
              <StatsCell
                text={app.cpu || ''}
                percent={parseCpuPercent(app.cpu)}
                isActive={isRun && !!app.cpu}
                isWarning={app.cpuThrottled}
                title={app.cpuThrottled ? t('table.cpu.throttled') : undefined}
              />
            </td>
            <td className="py-[3px] pr-4 text-right">
              <StatsCell
                text={app.memory ? app.memory.split(' / ')[0] : ''}
                percent={app.memoryPercent ?? 0}
                isActive={isRun && !!app.memory}
                isWarning={app.memoryWarning}
                isCritical={app.memoryCritical}
                title={app.memoryCritical ? t('table.memory.critical') : app.memoryWarning ? t('table.memory.warning') : undefined}
              />
            </td>
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
                <button type="button"
                  className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted disabled:hover:bg-transparent disabled:hover:text-muted-foreground disabled:opacity-40 disabled:cursor-not-allowed"
                  title={t('logs.title', { name: app.name })}
                  disabled={app.status === 'STOPPED'}
                  onClick={() => openSecondaryView({ id: `logs:${app.name}`, type: 'logs', title: t('logs.title', { name: app.name }), appName: app.name })}>
                  <FileText size={14} />
                </button>
                <button type="button" className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted relative"
                  title={t('table.cog.tooltip')}
                  onClick={() => openSecondaryView({ id: `app-config:${app.name}`, type: 'app-config', title: t('appConfig.tabTitle', { name: app.name }), appName: app.name })}
                  onContextMenu={async (e) => {
                    e.preventDefault()
                    const items = await buildCogMenu(app.name)
                    showContextMenu(e, items)
                  }}>
                  <Settings size={14} />
                  {app.edited && <Circle size={6} className="absolute top-0.5 right-0.5 fill-blue-500 text-blue-500" />}
                  {app.edited && app.locked && <Lock size={7} className="absolute bottom-0.5 right-0.5 text-blue-500" />}
                  {(app.editedFilesCount ?? 0) > 0 && (
                    <span className="absolute -bottom-0.5 -left-0.5 text-[8px] leading-none font-mono text-blue-500"
                      title={t('appConfig.fileEdited.badge')}>
                      {app.editedFilesCount}
                    </span>
                  )}
                </button>
              </div>
            </td>
          </tr>
        )
      })}
      {contextMenu && (
        <tr>
          <td colSpan={7}>
            <ContextMenu items={contextMenu.items} position={contextMenu.position} onClose={hideContextMenu} />
          </td>
        </tr>
      )}
      <RegistryCredentialsDialog
        open={credsFor !== null}
        host={credsFor?.host ?? ''}
        retryApp={credsFor?.app}
        onClose={handleCredsClose}
        onSaved={handleCredsSaved}
      />
    </>
  )
}

/**
 * Compact inline progress bar for image pull. Width ~64px so it slots next to
 * StatusBadge without bloating the row. Tooltip carries the full phase text.
 */
function PullProgressBar({ percent, phase }: { percent: number; phase: string }) {
  const pct = Math.max(0, Math.min(100, Math.round(percent)))
  return (
    <span className="inline-flex items-center gap-1 text-[10px] text-muted-foreground" title={phase}>
      <span className="inline-block h-1.5 w-16 rounded-full bg-muted overflow-hidden align-middle">
        <span
          className="block h-full bg-primary transition-[width] duration-200"
          style={{ width: `${pct}%` }}
        />
      </span>
      <span className="font-mono">{pct}%</span>
    </span>
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
