import { useEffect, useState, useCallback } from 'react'
import { useNavigate } from 'react-router'
import { useDashboardStore } from '../lib/store'
import { useTabsStore } from '../lib/tabs'
import { usePanelStore } from '../lib/panels'
import { openSecondaryView } from '../lib/desktop'
import { VolumesDialog } from '../components/VolumesDialog'
import { SecretsDialog } from '../components/SecretsDialog'
import { SnapshotsDialog } from '../components/SnapshotsDialog'
import { NamespaceDialog } from '../components/NamespaceDialog'
import { NamespaceEditDialog } from '../components/NamespaceEditDialog'
import { MasterPasswordDialog } from '../components/MasterPasswordDialog'
import { ContextMenu } from '../components/ContextMenu'
import { useContextMenu } from '../hooks/useContextMenu'
import { getSystemDump, getMigrationStatus, submitMasterPassword, unlockSecrets, setupSecretsPassword, resetSecrets, openExternal, getBundles, upgradeNamespace } from '../lib/api'
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
import { FormDialog } from '../components/FormDialog'
import type { BottomPanelTab } from '../lib/panels'
import { toast } from '../lib/toast'
import { ExternalLink, FolderOpen, Globe, Download, AlertTriangle, HardDrive, Key, Stethoscope, FileText, Settings, ArrowUpCircle, ArrowLeft } from 'lucide-react'
import { LoadingHint } from '../components/LoadingHint'
import { postOpenDir } from '../lib/api'

export function Dashboard() {
  // Selector-based subscriptions — Dashboard re-renders only when the fields
  // it consumes change, not on every SSE-triggered store mutation (e.g.
  // reconnectDelay / lastSeq / stream internal transitions).
  const namespace = useDashboardStore((s) => s.namespace)
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

  // Secret management dialog (Kotlin migration, setup, unlock)
  // Server mode: daemon auto-encrypts/auto-unlocks with default password, so
  // setup-password and unlock dialogs are never triggered. Only kotlin-decrypt
  // needs user input (original Kotlin master password).
  // Desktop mode: all three dialogs can appear.
  const [dialogStep, setDialogStep] = useState<'kotlin-decrypt' | 'setup-password' | 'unlock' | null>(null)
  const [dialogError, setDialogError] = useState('')
  const [dialogLoading, setDialogLoading] = useState(false)
  const [dialogChecked, setDialogChecked] = useState(false)

  // Upgrade dialog state
  const [upgradeOpen, setUpgradeOpen] = useState(false)
  const [upgradeVersions, setUpgradeVersions] = useState<{ label: string; value: string }[]>([])
  const [upgradeLoading, setUpgradeLoading] = useState(false)
  const [upgradeError, setUpgradeError] = useState<string | null>(null)

  // Modal dialog state — sidebar buttons open these as overlays (Kotlin parity)
  const [volumesDialogOpen, setVolumesDialogOpen] = useState(false)
  const [secretsDialogOpen, setSecretsDialogOpen] = useState(false)
  const [snapshotsDialogOpen, setSnapshotsDialogOpen] = useState(false)
  const [namespaceDialogOpen, setNamespaceDialogOpen] = useState(false)
  const [nsEditOpen, setNsEditOpen] = useState(false)

  // Context menu for the gear button (LMB → typed form, RMB → raw YAML).
  const { contextMenu, showContextMenu, hideContextMenu } = useContextMenu()

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

  const handleUpgradeClick = useCallback(async () => {
    try {
      const bundles = await getBundles()
      const currentRef = namespace?.bundleRef ?? ''
      const options: { label: string; value: string }[] = []
      for (const b of bundles) {
        for (const v of b.versions) {
          const ref = `${b.repo}:${v}`
          if (ref !== currentRef) {
            options.push({ label: ref, value: ref })
          }
        }
      }
      if (options.length === 0) {
        toast(t('upgrade.alreadyLatest'), 'info')
        return
      }
      setUpgradeVersions(options)
      setUpgradeError(null)
      setUpgradeOpen(true)
    } catch (e) {
      toast((e as Error).message, 'error')
    }
  }, [namespace?.bundleRef, t])

  // On mount: detect which dialog step is needed.
  // Server mode: daemon auto-encrypts + auto-unlocks with default password, so only
  // kotlin-decrypt (Kotlin migration) ever triggers. Desktop mode: all three possible.
  useEffect(() => {
    if (!namespace) {
      // Daemon/namespace dropped (e.g. restart). Reset the guard so the check
      // re-fires when a namespace comes back.
      if (dialogChecked) setDialogChecked(false)
      return
    }
    if (dialogChecked || dialogStep) return
    setDialogChecked(true)
    getMigrationStatus().then((s) => {
      if (s.hasPendingSecrets) setDialogStep('kotlin-decrypt')
      else if (s.encrypted && s.locked) setDialogStep('unlock')
      else if (!s.encrypted && s.hasSecrets) setDialogStep('setup-password')
    }).catch(() => {})
  }, [namespace, dialogChecked, dialogStep])

  // Kotlin decrypt → import + encrypt secrets in one step
  const handleKotlinDecrypt = useCallback(async (pwd: string) => {
    if (!pwd) return
    setDialogLoading(true)
    setDialogError('')
    try {
      await submitMasterPassword(pwd)
      toast(t('migration.secretsImported'), 'success')
      setDialogStep(null)
      fetchData()
    } catch {
      setDialogError(t('migration.wrongPassword'))
    } finally {
      setDialogLoading(false)
    }
  }, [fetchData, t])

  // Setup password — encrypt all secrets (desktop mode only)
  const handleSetupPassword = useCallback(async (pwd: string) => {
    setDialogLoading(true)
    setDialogError('')
    try {
      await setupSecretsPassword(pwd)
      toast(t('migration.setupPassword.success'), 'success')
      setDialogStep(null)
      fetchData()
    } catch (e) {
      setDialogError((e as Error).message)
    } finally {
      setDialogLoading(false)
    }
  }, [fetchData, t])

  // Unlock encrypted secrets (desktop mode only)
  const handleUnlock = useCallback(async (pwd: string) => {
    if (!pwd) return
    setDialogLoading(true)
    setDialogError('')
    try {
      await unlockSecrets(pwd)
      toast(t('migration.unlock.success'), 'success')
      setDialogStep(null)
      fetchData()
    } catch {
      setDialogError(t('migration.wrongPassword'))
    } finally {
      setDialogLoading(false)
    }
  }, [fetchData, t])

  const handleSkipDialog = useCallback(() => {
    setDialogStep(null)
    setDialogError('')
  }, [])

  useEffect(() => {
    setHomeTab(t('dashboard.title'))
    fetchData()
    startEventStream()
    return () => stopEventStream()
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
  // Aggregate caps for the sidebar progress bars (Kotlin CompactResourceRow):
  // CPU max = runningApps × 100 (each container can use a whole core).
  // MEM max = sum of per-app memory limits parsed from "used / limit".
  const maxCpu = runningApps.length * 100
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
      {/* Master password modal — kotlin-decrypt / create / ask (Kotlin parity) */}
      <MasterPasswordDialog
        mode={dialogStep === 'setup-password' ? 'create' : dialogStep === 'unlock' ? 'ask' : 'kotlin-decrypt'}
        open={!!dialogStep}
        loading={dialogLoading}
        error={dialogError}
        onSubmit={async (pwd) => {
          if (dialogStep === 'kotlin-decrypt') await handleKotlinDecrypt(pwd)
          else if (dialogStep === 'setup-password') await handleSetupPassword(pwd)
          else if (dialogStep === 'unlock') await handleUnlock(pwd)
        }}
        onSkip={(dialogStep === 'kotlin-decrypt' || dialogStep === 'unlock') ? handleSkipDialog : undefined}
        onReset={dialogStep === 'unlock' ? async () => {
          setDialogLoading(true)
          try {
            await resetSecrets()
            toast(t('migration.unlock.reset.success'), 'success')
            setDialogStep(null)
          } catch (e) { setDialogError((e as Error).message) }
          finally { setDialogLoading(false) }
        } : undefined}
      />

      {/* Top: sidebar + table + drawer overlay */}
      <div className="flex flex-1 min-h-0 relative">
        {/* Left info panel */}
        <aside className="w-60 shrink-0 border-r border-border bg-card flex flex-col h-full">
          {/* Scrollable content */}
          <div className="flex-1 min-h-0 overflow-y-auto p-3 flex flex-col gap-2">
          <div className="flex items-center justify-between">
            <button
              type="button"
              className="min-w-0 text-left hover:bg-muted/30 -mx-1 px-1 py-0.5 rounded"
              title={t('namespaces.switch')}
              onClick={() => setNamespaceDialogOpen(true)}
            >
              <div className="text-sm font-semibold truncate">
                {namespace.name}
                <span className="ml-1 font-normal text-muted-foreground">({namespace.id})</span>
              </div>
              <div className="text-[11px] text-muted-foreground mt-0.5 truncate">{namespace.bundleRef}</div>
            </button>
            <div className="flex items-center gap-0.5 shrink-0">
              <button
                type="button"
                className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted"
                title={t('upgrade.title')}
                onClick={handleUpgradeClick}
              >
                <ArrowUpCircle size={14} />
              </button>
              <button
                type="button"
                className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted"
                title={t('dashboard.nsConfig')}
                onClick={() => setNsEditOpen(true)}
                onContextMenu={(e) => showContextMenu(e, [
                  {
                    label: t('nsEdit.title'),
                    onClick: () => setNsEditOpen(true),
                  },
                  {
                    label: t('nsEdit.editRawYaml'),
                    onClick: () => openBottomTab({ id: 'ns-config', type: 'ns-config', title: t('configEditor.title') }),
                  },
                ])}
              >
                <Settings size={14} />
              </button>
            </div>
          </div>

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
          <div className="shrink-0 p-3 pt-2 border-t border-border flex flex-col gap-1">
            {/* Kotlin parity (NamespaceScreen.kt:308-324): back-to-Welcome arrow,
                only enabled when no apps are running so the user can't strand
                containers by switching namespaces mid-flight. */}
            <SidebarBtn icon={ArrowLeft}
              label={t('dashboard.backToWelcome')}
              tooltip={namespace.status === 'STOPPED' ? t('dashboard.backToWelcome') : t('dashboard.backToWelcome.disabled')}
              disabled={namespace.status !== 'STOPPED'}
              onClick={() => setNamespaceDialogOpen(true)} />
            <SidebarBtn icon={HardDrive} label={t('dashboard.volumes')}
              onClick={() => setVolumesDialogOpen(true)} />
            {/* Open NS Dir — Kotlin parity (NamespaceScreen.kt sidebar
                "Open Namespace Dir"). Desktop mode shells out to the OS file
                manager; server mode returns the path so the user can open it
                manually on the daemon host. */}
            <SidebarBtn icon={FolderOpen} label={t('dashboard.openNsDir')}
              tooltip={t('dashboard.openNsDir.tooltip')}
              onClick={handleOpenNsDir} />
            <SidebarBtn icon={Key} label={t('dashboard.secrets')}
              onClick={() => setSecretsDialogOpen(true)} />
            <SidebarBtn icon={Stethoscope} label={t('dashboard.diagnostics')}
              onClick={() => { openTab({ id: 'diagnostics', title: t('dashboard.diagnostics'), path: '/diagnostics' }); navigate('/diagnostics') }} />
            <SidebarBtn icon={AlertTriangle} label={t('dashboard.restartEvents')}
              onClick={() => openBottomTab({ id: 'restart-events', type: 'restart-events', title: t('dashboard.restartEvents') })} />
            <SidebarBtn icon={FileText} label={t('dashboard.launcherLogs')}
              onClick={() => openSecondaryView({ id: 'daemon-logs', type: 'daemon-logs', title: t('daemonLogs.title') })} />
            <SidebarBtn icon={Download} label={t('dashboard.systemDump')}
              onClick={() => getSystemDump('zip').then(() => toast(t('dashboard.systemDump.success'), 'success')).catch((e) => toast((e as Error).message, 'error'))} />
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

      {/* Upgrade dialog */}
      <FormDialog
        title={t('upgrade.title')}
        fields={[{
          key: 'bundleRef',
          label: t('upgrade.select'),
          type: 'select',
          required: true,
          options: upgradeVersions,
        }]}
        onSubmit={async (data) => {
          setUpgradeLoading(true)
          setUpgradeError(null)
          try {
            await upgradeNamespace(data.bundleRef as string)
            toast(t('upgrade.success'), 'success')
            setUpgradeOpen(false)
            fetchData()
          } catch (e) {
            setUpgradeError((e as Error).message)
          } finally {
            setUpgradeLoading(false)
          }
        }}
        onCancel={() => setUpgradeOpen(false)}
        open={upgradeOpen}
        loading={upgradeLoading}
        error={upgradeError}
      />

      {/* Sidebar-opened modal dialogs (Kotlin parity) */}
      <VolumesDialog
        open={volumesDialogOpen}
        onClose={() => setVolumesDialogOpen(false)}
        onOpenSnapshots={() => { setVolumesDialogOpen(false); setSnapshotsDialogOpen(true) }}
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

function SidebarBtn({ icon: Icon, label, tooltip, onClick, disabled }: { icon: React.ElementType; label: string; tooltip?: string; onClick?: () => void; disabled?: boolean }) {
  return (
    <button type="button"
      disabled={disabled}
      className={`flex items-center gap-1.5 text-xs py-1 px-1 rounded ${
        disabled ? 'text-muted-foreground/40 cursor-not-allowed' : 'text-muted-foreground hover:text-foreground hover:bg-muted'
      }`}
      onClick={onClick} title={tooltip ?? label}>
      <Icon size={13} />
      {label}
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
      // eslint-disable-next-line no-console
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
