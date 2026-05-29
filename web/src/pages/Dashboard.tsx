import { useEffect, useState, useCallback } from 'react'
import { subscribeRefresh } from '../lib/windowBus'
import { useNavigate } from 'react-router'
import { useDashboardStore } from '../lib/store'
import { useIsDesktop } from '../lib/daemonStatus'
import { useTabsStore } from '../lib/tabs'
import { usePanelStore } from '../lib/panels'
import { openSecondaryView } from '../lib/desktop'
import { VolumesDialog } from '../components/VolumesDialog'
import { SecretsDialog } from '../components/SecretsDialog'
import { SnapshotsDialog } from '../components/SnapshotsDialog'
import { NamespaceDialog } from '../components/NamespaceDialog'
import { NamespaceEditDialog } from '../components/NamespaceEditDialog'
import { ContextMenu } from '../components/ContextMenu'
import { useContextMenu } from '../hooks/useContextMenu'
import { getSystemDump, openExternal, deactivateNamespace } from '../lib/api'
import { useTranslation } from '../lib/i18n'
import { StatusBadge } from '../components/StatusBadge'
import { AppTable } from '../components/AppTable'
import { CompactResourceRow } from '../components/CompactResourceRow'
import { NamespaceControls } from '../components/NamespaceControls'
import { BottomPanel } from '../components/BottomPanel'
import { RightDrawer } from '../components/RightDrawer'
import { AppDrawerContent } from '../components/AppDrawerContent'
import { LogViewer } from '../components/LogViewer'
import { ConfigEditor } from '../components/ConfigEditor'
import { DaemonLogsViewer } from '../components/DaemonLogsViewer'
import { AppConfigEditor } from '../components/AppConfigEditor'
import { RestartEvents } from '../components/RestartEvents'
import type { BottomPanelTab } from '../lib/panels'
import { toast } from '../lib/toast'
import { ExternalLink, FolderOpen, Globe, Download, AlertTriangle, HardDrive, Key, Stethoscope, FileText, ArrowLeft } from 'lucide-react'
import { LoadingHint } from '../components/LoadingHint'
import { postOpenDir } from '../lib/api'

export function Dashboard() {
  // Selector-based subscriptions — Dashboard re-renders only when the fields
  // it consumes change, not on every SSE-triggered store mutation (e.g.
  // reconnectDelay / lastSeq / stream internal transitions).
  const namespace = useDashboardStore((s) => s.namespace)
  const isDesktop = useIsDesktop()
  const loading = useDashboardStore((s) => s.loading)
  const error = useDashboardStore((s) => s.error)
  const fetchData = useDashboardStore((s) => s.fetchData)
  const startEventStream = useDashboardStore((s) => s.startEventStream)
  const stopEventStream = useDashboardStore((s) => s.stopEventStream)
  const setHomeTab = useTabsStore((s) => s.setHomeTab)
  const drawerAppName = usePanelStore((s) => s.drawerAppName)
  const closeDrawer = usePanelStore((s) => s.closeDrawer)
  const bottomTabs = usePanelStore((s) => s.bottomTabs)
  const openBottomTab = usePanelStore((s) => s.openBottomTab)
  const { t } = useTranslation()

  // Master-password / secrets-unlock flow is handled by SecretsUnlockGuard at
  // the App layout level — runs once before any namespace start so registry
  // pulls have access to credentials.

  // Modal dialog state — sidebar buttons open these as overlays (Kotlin parity)
  const [volumesDialogOpen, setVolumesDialogOpen] = useState(false)
  const [secretsDialogOpen, setSecretsDialogOpen] = useState(false)
  const [snapshotsDialogOpen, setSnapshotsDialogOpen] = useState(false)
  // nsEditOpen + nsSwitcherOpen live in the panel store so the global TabBar
  // can open them without prop-drilling through Dashboard.
  const nsEditOpen = usePanelStore((s) => s.nsEditOpen)
  const setNsEditOpen = usePanelStore((s) => s.setNsEditOpen)
  const namespaceDialogOpen = usePanelStore((s) => s.nsSwitcherOpen)
  const setNamespaceDialogOpen = usePanelStore((s) => s.setNsSwitcherOpen)

  // Context menu state still owned by Dashboard for in-page right-clicks
  // (the gear's context menu lives in TabBar). showContextMenu omitted —
  // intentional: no caller here anymore.
  const { contextMenu, hideContextMenu } = useContextMenu()

  const handleOpenNsDir = useCallback(async () => {
    try {
      const resp = await postOpenDir('volumes')
      if (resp.opened) {
        toast(t('dashboard.openNsDir.success', { path: resp.path }), 'success')
      } else {
        // Server mode (or desktop fallback): show the path so the user can
        // open it manually. Use 'info' so it's visually distinct from an
        // error — this is the documented server-mode behavior.
        toast(t('dashboard.openNsDir.serverInfo', { path: resp.path }), 'info')
      }
    } catch (e) {
      toast(t('dashboard.openNsDir.failed', { error: (e as Error).message }), 'error')
    }
  }, [t])

  useEffect(() => {
    setHomeTab(t('dashboard.title'))
    fetchData()
    startEventStream()
    // Cross-window refresh ping: secondary editor windows post a message
    // after a successful save so the dashboard can refetch immediately,
    // bypassing Wails' background-window EventSource throttling.
    const unsub = subscribeRefresh(() => fetchData())
    // Manual refresh: Wails doesn't pass through the browser-default F5
    // reload, so dashboard refetch is exposed as an explicit shortcut.
    const onF5 = (e: KeyboardEvent) => {
      if (e.key === 'F5' || (e.ctrlKey && e.code === 'KeyR' && !e.shiftKey)) {
        e.preventDefault()
        fetchData()
      }
    }
    window.addEventListener('keydown', onF5)
    return () => { unsub(); stopEventStream(); window.removeEventListener('keydown', onF5) }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- store methods are stable
  }, [])

  // Escape key: close drawer first, then collapse bottom panel.
  // Skip if an input/textarea is focused (LogViewer search uses Escape to clear).
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key !== 'Escape') return
      const tag = (e.target as HTMLElement)?.tagName
      if (tag === 'INPUT' || tag === 'TEXTAREA') return
      const { drawerAppName, bottomPanelOpen, closeDrawer, toggleBottomPanel } = usePanelStore.getState()
      if (drawerAppName) {
        closeDrawer()
      } else if (bottomPanelOpen) {
        toggleBottomPanel()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [])

  const navigate = useNavigate()
  const openTab = useTabsStore((s) => s.openTab)

  if (loading && !namespace) {
    return (
      <div className="flex flex-col items-center justify-center h-full w-full">
        <div className="text-2xl text-foreground">{t('common.loading')}</div>
        <LoadingHint active={loading} />
      </div>
    )
  }

  if (error && !namespace) {
    return <div className="text-destructive text-xs p-4">{t('dashboard.error', { error })}</div>
  }

  if (!namespace) return null

  const apps = namespace.apps ?? []
  const runningCount = apps.filter((a) => a.status === 'RUNNING').length
  const isRunning = namespace.status === 'RUNNING'
  const links = namespace.links ? [...namespace.links].sort((a, b) => a.order - b.order) : []
  const proxyUrl = links.find((l) => l.name === 'Citeck UI')?.url
  const serviceLinks = links.filter((l) => l.name !== 'Citeck UI')

  const runningApps = apps.filter((a) => a.status === 'RUNNING')
  const totalCpu = runningApps.reduce((sum, a) => sum + (parseFloat(a.cpu) || 0), 0)
  const totalMem = runningApps.reduce((sum, a) => {
    const m = a.memory?.split(' / ')[0]
    if (!m) return sum
    if (m.endsWith('G')) return sum + parseFloat(m) * 1024
    if (m.endsWith('M')) return sum + parseFloat(m)
    return sum
  }, 0)
  // Aggregate caps for the sidebar progress bars:
  //   CPU max = host CPU cores × 100 (Docker per-container stats span all
  //   cores, so the meaningful aggregate ceiling is host capacity, not
  //   apps × 100 which over-counts by N). hostCpus comes from the daemon
  //   (runtime.NumCPU); fall back to the old wrong-but-non-zero formula
  //   only when the daemon hasn't reported a value yet.
  //   MEM max = sum of per-app memory limits parsed from "used / limit".
  const maxCpu = (namespace.hostCpus ?? runningApps.length) * 100
  const maxMem = runningApps.reduce((sum, a) => {
    const m = a.memory?.split(' / ')[1]
    if (!m) return sum
    if (m.endsWith('G')) return sum + parseFloat(m) * 1024
    if (m.endsWith('M')) return sum + parseFloat(m)
    return sum
  }, 0)
  const cpuPercent = maxCpu > 0 ? (totalCpu / maxCpu) * 100 : 0
  const memPercent = maxMem > 0 ? (totalMem / maxMem) * 100 : 0
  const fmtMB = (mb: number) => mb >= 1024 ? `${(mb / 1024).toFixed(1)}G` : `${Math.round(mb)}M`

  // Drawer title info
  const drawerApp = drawerAppName ? apps.find((a) => a.name === drawerAppName) : null

  function renderBottomTab(tab: BottomPanelTab, active: boolean) {
    switch (tab.type) {
      case 'logs':
        return <LogViewer appName={tab.appName!} compact active={active} />
      case 'ns-config':
        return <ConfigEditor compact />
      case 'daemon-logs':
        return <DaemonLogsViewer compact active={active} />
      case 'app-config':
        return <AppConfigEditor appName={tab.appName!} />
      case 'restart-events':
        return <RestartEvents active={active} />
      default:
        return null
    }
  }

  return (
    <div className="flex flex-col flex-1 min-h-0 overflow-hidden">
      {/* Top: sidebar + table + drawer overlay */}
      <div className="flex flex-1 min-h-0 relative">
        {/* Left info panel */}
        <aside className="w-60 shrink-0 border-r border-border bg-card flex flex-col h-full">
          {/* Scrollable content */}
          <div className="flex-1 min-h-0 overflow-y-auto p-3 flex flex-col gap-2">
          <div className="flex items-center gap-2">
            <StatusBadge status={namespace.status} variant="indicator" />
            <span className="text-xs text-muted-foreground">{runningCount}/{apps.length}</span>
          </div>

          <div className="space-y-0.5">
            <CompactResourceRow
              label={t('dashboard.cpu')}
              used={runningApps.length > 0 ? `${totalCpu.toFixed(1)}%` : '-'}
              total={runningApps.length > 0 && maxCpu > 0 ? `${maxCpu}%` : undefined}
              percent={cpuPercent}
              throttled={runningApps.some((a) => a.cpuThrottled)}
              inactive={runningApps.length === 0}
            />
            <CompactResourceRow
              label={t('dashboard.mem')}
              used={runningApps.length > 0 ? fmtMB(totalMem) : '-'}
              total={runningApps.length > 0 && maxMem > 0 ? fmtMB(maxMem) : undefined}
              percent={memPercent}
              inactive={runningApps.length === 0}
            />
          </div>

          <NamespaceControls status={namespace.status} />

          {proxyUrl && (
            <button
              type="button"
              className={`flex items-center gap-1.5 rounded border px-2 py-1.5 text-xs ${
                isRunning
                  ? 'border-primary/40 text-primary hover:bg-primary/10 cursor-pointer'
                  : 'border-border text-muted-foreground cursor-not-allowed opacity-50'
              }`}
              onClick={() => { if (isRunning) openExternal(proxyUrl) }}
              title={openInBrowserTooltip(namespace.status, t)}
            >
              <Globe size={14} />
              {t('dashboard.openInBrowser')}
            </button>
          )}

          {serviceLinks.length > 0 && (
            <div>
              <div className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-1">{t('dashboard.links')}</div>
              {/* Kotlin parity: group by `category`. The first link in a new
                  category gets a small header. Links without a category render
                  before the first header (matching the alwaysEnabled=true /
                  category=undefined case). */}
              <div className="flex flex-col gap-0.5">
                {serviceLinks.map((l, i) => {
                  const prevCategory = i > 0 ? (serviceLinks[i - 1].category ?? '') : '__INIT__'
                  const showHeader = (l.category ?? '') !== prevCategory && l.category
                  const alwaysOn = isLinkAlwaysEnabled(l)
                  const enabled = isRunning || alwaysOn
                  return (
                    <div key={l.name}>
                      {showHeader && (
                        <div className="text-[10px] text-muted-foreground/80 mt-1.5 mb-0.5">{l.category}</div>
                      )}
                      <a href={l.url} target="_blank" rel="noopener noreferrer"
                        title={l.description ?? l.name}
                        className={`flex items-center gap-1.5 text-xs py-0.5 ${
                          enabled ? 'text-primary hover:underline' : 'text-muted-foreground cursor-not-allowed'
                        }`}
                        onClick={(e) => {
                          e.preventDefault()
                          if (!enabled) return
                          openExternal(l.url)
                        }}>
                        {l.icon
                          ? <img src={`/icons/${l.icon}.svg`} alt="" width={12} height={12} className="opacity-80" onError={(e) => { (e.currentTarget as HTMLImageElement).style.display = 'none' }} />
                          : <ExternalLink size={11} />}
                        {l.name}
                      </a>
                    </div>
                  )
                })}
              </div>
            </div>
          )}

          </div>
          {/* Fixed footer — always visible at bottom */}
          <div className="shrink-0 p-3 pt-2 border-t border-border flex flex-row flex-wrap items-center gap-1">
            {/* Kotlin parity (NamespaceScreen.kt:308-324): back-to-Welcome arrow,
                only enabled when no apps are running so the user can't strand
                containers by switching namespaces mid-flight. */}
            {/* Kotlin parity (NamespaceScreen.kt sidebar footer): icon-only row
                with tooltips, ordered: Back | Open Dir | Launcher Logs |
                Volumes | Secrets | System Dump | Diagnostics | Restart Events. */}
            {isDesktop && (
              <SidebarIconBtn icon={ArrowLeft}
                tooltip={namespace.status === 'STOPPED' ? t('dashboard.backToWelcome') : t('dashboard.backToWelcome.disabled')}
                disabled={namespace.status !== 'STOPPED'}
                onClick={async () => {
                  // Tell the daemon to drop the selection — otherwise the
                  // next launcher start auto-loads this namespace again.
                  try {
                    await deactivateNamespace()
                  } catch (e) {
                    toast((e as Error).message, 'error')
                    return
                  }
                  navigate('/welcome')
                }} />
            )}
            <SidebarIconBtn icon={FolderOpen}
              tooltip={t('dashboard.openNsDir.tooltip')}
              onClick={handleOpenNsDir} />
            <SidebarIconBtn icon={FileText}
              tooltip={t('dashboard.launcherLogs')}
              onClick={() => openSecondaryView({ id: 'daemon-logs', type: 'daemon-logs', title: t('daemonLogs.title') })} />
            <SidebarIconBtn icon={HardDrive}
              tooltip={t('dashboard.volumes')}
              onClick={() => setVolumesDialogOpen(true)} />
            <SidebarIconBtn icon={Key}
              tooltip={t('dashboard.secrets')}
              onClick={() => setSecretsDialogOpen(true)} />
            <SidebarIconBtn icon={AlertTriangle}
              tooltip={t('dashboard.systemDump')}
              onClick={() => getSystemDump('zip').then(() => toast(t('dashboard.systemDump.success'), 'success')).catch((e) => toast((e as Error).message, 'error'))} />
            <SidebarIconBtn icon={Stethoscope}
              tooltip={t('dashboard.diagnostics')}
              onClick={() => { openTab({ id: 'diagnostics', title: t('dashboard.diagnostics'), path: '/diagnostics' }); navigate('/diagnostics') }} />
            <SidebarIconBtn icon={Download}
              tooltip={t('dashboard.restartEvents')}
              onClick={() => openBottomTab({ id: 'restart-events', type: 'restart-events', title: t('dashboard.restartEvents') })} />
          </div>
        </aside>

        {/* Main table area */}
        <div className="flex-1 min-w-0 p-2 overflow-auto">
          <AppTable apps={apps} highlightedApp={drawerAppName} />
        </div>

        {/* Right drawer overlay */}
        {drawerAppName && (
          <RightDrawer
            title={drawerAppName}
            subtitle={drawerApp && <StatusBadge status={drawerApp.status} />}
            onClose={closeDrawer}
          >
            <AppDrawerContent appName={drawerAppName} />
          </RightDrawer>
        )}
      </div>

      {/* Bottom panel */}
      {bottomTabs.length > 0 && <BottomPanel renderTab={renderBottomTab} />}

      {/* Sidebar-opened modal dialogs (Kotlin parity) */}
      <VolumesDialog
        open={volumesDialogOpen}
        onClose={() => setVolumesDialogOpen(false)}
        onOpenSnapshots={() => setSnapshotsDialogOpen(true)}
        namespaceStopped={namespace?.status === 'STOPPED'}
      />
      <SnapshotsDialog
        open={snapshotsDialogOpen}
        onClose={() => setSnapshotsDialogOpen(false)}
        namespaceStopped={namespace?.status === 'STOPPED'}
      />
      <SecretsDialog
        open={secretsDialogOpen}
        onClose={() => setSecretsDialogOpen(false)}
      />
      <NamespaceDialog
        open={namespaceDialogOpen}
        onClose={() => setNamespaceDialogOpen(false)}
      />
      <NamespaceEditDialog
        open={nsEditOpen}
        mode="edit"
        onClose={() => setNsEditOpen(false)}
      />
      {contextMenu && (
        <ContextMenu
          items={contextMenu.items}
          position={contextMenu.position}
          onClose={hideContextMenu}
        />
      )}
    </div>
  )
}

function SidebarIconBtn({ icon: Icon, tooltip, onClick, disabled }: { icon: React.ElementType; tooltip: string; onClick?: () => void; disabled?: boolean }) {
  return (
    <button type="button"
      disabled={disabled}
      className={`flex items-center justify-center w-7 h-7 rounded ${
        disabled ? 'text-muted-foreground/40 cursor-not-allowed' : 'text-muted-foreground hover:text-foreground hover:bg-muted'
      }`}
      onClick={onClick} title={tooltip}>
      <Icon size={16} />
    </button>
  )
}

// Older daemon builds (or workspace configs that haven't been migrated yet) don't
// populate `alwaysEnabled` — fall back to the original order>=100 convention
// (GlobalLinks in Kotlin) and warn once so the deprecation is visible.
const linkOrderFallbackLogged = new Set<string>()
function isLinkAlwaysEnabled(l: { alwaysEnabled?: boolean; order: number; name: string }): boolean {
  if (l.alwaysEnabled !== undefined) return l.alwaysEnabled
  if (l.order >= 100) {
    if (!linkOrderFallbackLogged.has(l.name)) {
      linkOrderFallbackLogged.add(l.name)
       
      console.warn(`[deprecation] sidebar link "${l.name}" uses order>=100 fallback for alwaysEnabled — daemon should set links[].alwaysEnabled=true.`)
    }
    return true
  }
  return false
}

// Kotlin parity (NamespaceScreen.kt) — per-status tooltip on Open In Browser.
function openInBrowserTooltip(status: string, t: (key: string) => string): string {
  switch (status) {
    case 'STARTING':
      return t('dashboard.openInBrowser.starting')
    case 'STALLED':
      return t('dashboard.openInBrowser.stalled')
    case 'RUNNING':
      return t('dashboard.openInBrowser.tooltip')
    default:
      return t('dashboard.openInBrowser.disabled')
  }
}
