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
  const groups = groupByKind(apps)
  const { t } = useTranslation()

  // Start / stop / restart fire immediately — Kotlin parity. The
  // ConfirmModal that used to gate every click added a click-cost the user
  // had to pay for actions they had already explicitly clicked.
  const runAction = async (a: NonNullable<AppAction>) => {
    try {
      if (a.type === 'stop') await postAppStop(a.appName)
      else if (a.type === 'start') await postAppStart(a.appName)
      else await postAppRestart(a.appName)
      toast(t('table.toast.success', { action: a.type.charAt(0).toUpperCase() + a.type.slice(1), name: a.appName }), 'success')
    } catch (err) {
      toast((err as Error).message, 'error')
    }
  }

  return (
    <>
      {/* whitespace-nowrap inherits to every cell — long status text / labels
          truncate within their fixed column instead of wrapping and inflating
          row height. */}
      <table className="w-full text-[13px] leading-tight border-collapse table-fixed whitespace-nowrap">
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
        {/* Sticky header — the app list scrolls (24 rows on enterprise), so the
            column labels stay pinned to the top of the scroll container. bg +
            bottom border live on the th cells (a sticky <tr>'s own border can
            disappear under the scrolling body). */}
        <thead>
          <tr className="text-left text-[11px] uppercase tracking-wide text-muted-foreground">
            {[
              { k: 'table.name', c: 'pr-4' },
              { k: 'table.status', c: 'pr-4' },
              { k: 'table.cpu', c: 'pr-2 text-right' },
              { k: 'table.mem', c: 'pr-4 text-right' },
              { k: 'table.ports', c: 'pr-4' },
              { k: 'table.tag', c: 'pr-4' },
              { k: 'table.actions', c: 'text-right' },
            ].map((col) => (
              <th
                key={col.k}
                className={`sticky top-0 z-10 bg-background py-1.5 font-medium shadow-[inset_0_-1px_0_var(--color-border)] ${col.c}`}
              >
                {t(col.k)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {groups.map((g) => (
            <GroupRows key={g.kind} labelKey={g.labelKey} apps={g.apps} onAction={runAction} highlightedApp={highlightedApp} />
          ))}
        </tbody>
      </table>
    </>
  )
}

function GroupRows({ labelKey, apps, onAction, highlightedApp }: { labelKey: string; apps: AppDto[]; onAction: (a: NonNullable<AppAction>) => void; highlightedApp?: string | null }) {
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
        .map((f) => {
          // Show just the basename in the menu — Kotlin parity. The full
          // bind-mount path stays in the title attribute for the user who
          // needs to disambiguate two same-named files from different dirs.
          const slash = f.path.lastIndexOf('/')
          const basename = slash >= 0 ? f.path.slice(slash + 1) : f.path
          return {
            label: basename,
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
              title: t('appConfig.tabTitle', { name: `${appName} — ${basename}` }),
              appName,
              filePath: f.path,
            }),
          }
        })
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
        {/* Section anchor — the old launcher used bold headers as the primary
            grouping signal. A label + trailing hairline keeps that clarity
            without the heavy weight, so the eye can find group boundaries in a
            24-row list. */}
        <td colSpan={7} className="pt-3.5 pb-1">
          <div className="flex items-center gap-2.5">
            <span className="text-[11px] font-semibold uppercase tracking-wider text-foreground/65">
              {t(labelKey)}
            </span>
            <span className="h-px flex-1 bg-border/70" aria-hidden="true" />
          </div>
        </td>
      </tr>
      {apps.map((app) => {
        const isRun = RUNNING.includes(app.status)
        const isStop = STOPPED.includes(app.status)
        const isTransitional = TRANSITIONAL.includes(app.status)
        const isHighlighted = highlightedApp === app.name
        return (
          // Whole row opens the inspect drawer; the actions cell and the
          // tag-copy cell stop propagation so their own clicks still win.
          <tr key={app.name}
            className={`group cursor-pointer border-b border-border/20 ${isHighlighted ? 'bg-primary/8' : 'hover:bg-accent'}`}
            onClick={() => openDrawer(app.name)}>
            <td className="py-[2px] pr-4 font-mono whitespace-nowrap">
              <span className="text-primary group-hover:underline">{app.name}</span>
            </td>
            <td className="py-[2px] pr-4 overflow-hidden">
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
            <td className="py-[2px] pr-2 text-right">
              <StatsCell
                text={app.cpu || ''}
                percent={parseCpuPercent(app.cpu)}
                isActive={isRun && !!app.cpu}
                isWarning={app.cpuThrottled}
                title={app.cpuThrottled ? t('table.cpu.throttled') : undefined}
              />
            </td>
            <td className="py-[2px] pr-4 text-right">
              <StatsCell
                text={app.memory ? app.memory.split(' / ')[0] : ''}
                percent={app.memoryPercent ?? 0}
                isActive={isRun && !!app.memory}
                isWarning={app.memoryWarning}
                isCritical={app.memoryCritical}
                title={app.memoryCritical ? t('table.memory.critical') : app.memoryWarning ? t('table.memory.warning') : undefined}
              />
            </td>
            <td className="py-[2px] pr-4 font-mono text-muted-foreground whitespace-nowrap" title={app.ports?.join(', ')}>
              {portsShort(app.ports)}
            </td>
            <td className="py-[2px] pr-4 font-mono text-muted-foreground whitespace-nowrap cursor-pointer hover:text-foreground"
              title={t('table.copy', { image: app.image })}
              onClick={(e) => { e.stopPropagation(); navigator.clipboard.writeText(app.image) }}>
              {tag(app.image)}
            </td>
            <td className="py-[2px] text-right whitespace-nowrap" onClick={(e) => e.stopPropagation()}>
              <div className="inline-flex items-center gap-0.5">
                {isRun && (
                  <>
                    <IconBtn icon={Square} filled title={t('table.action.stop')} color="hover:text-destructive" onClick={() => onAction({ type: 'stop', appName: app.name })} />
                    <IconBtn icon={RotateCw} title={t('table.action.restart')} onClick={() => onAction({ type: 'restart', appName: app.name })} />
                  </>
                )}
                {isStop && (
                  <IconBtn icon={Play} filled title={t('table.action.start')} color="hover:text-success" onClick={() => onAction({ type: 'start', appName: app.name })} />
                )}
                {isTransitional && (
                  <IconBtn icon={Square} filled title={t('table.action.stop')} color="hover:text-destructive" onClick={() => onAction({ type: 'stop', appName: app.name })} />
                )}
                <button type="button"
                  className="px-1 py-0.5 rounded text-muted-foreground hover:text-foreground hover:bg-muted disabled:hover:bg-transparent disabled:hover:text-muted-foreground disabled:opacity-40 disabled:cursor-not-allowed"
                  title={t('logs.title', { name: app.name })}
                  disabled={app.status === 'STOPPED'}
                  onClick={() => openSecondaryView({ id: `logs:${app.name}`, type: 'logs', title: t('logs.title', { name: app.name }), appName: app.name })}>
                  <FileText size={14} />
                </button>
                <button type="button" className="px-1 py-0.5 rounded text-muted-foreground hover:text-foreground hover:bg-muted relative"
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

function IconBtn({ icon: Icon, title, color, onClick, filled }: { icon: React.ElementType; title: string; color?: string; onClick: () => void; filled?: boolean }) {
  return (
    <button
      type="button"
      className={`px-1 py-0.5 rounded text-muted-foreground ${color ?? 'hover:text-foreground'} hover:bg-muted`}
      onClick={onClick}
      title={title}
    >
      {/* Transport-style controls (stop/play) render filled so the stop button
          reads as a solid square, not a hollow checkbox. */}
      <Icon size={14} className={filled ? 'fill-current' : undefined} />
    </button>
  )
}
