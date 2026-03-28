import { useEffect } from 'react'
import { useNavigate } from 'react-router'
import { useDashboardStore } from '../lib/store'
import { useTabsStore } from '../lib/tabs'
import { usePanelStore } from '../lib/panels'
import { getSystemDump } from '../lib/api'
import { useTranslation } from '../lib/i18n'
import { StatusBadge } from '../components/StatusBadge'
import { AppTable } from '../components/AppTable'
import { NamespaceControls } from '../components/NamespaceControls'
import { BottomPanel } from '../components/BottomPanel'
import { RightDrawer } from '../components/RightDrawer'
import { AppDrawerContent } from '../components/AppDrawerContent'
import { LogViewer } from '../components/LogViewer'
import { ConfigEditor } from '../components/ConfigEditor'
import { DaemonLogsViewer } from '../components/DaemonLogsViewer'
import { AppConfigEditor } from '../components/AppConfigEditor'
import type { BottomPanelTab } from '../lib/panels'
import { toast } from '../lib/toast'
import { ExternalLink, Globe, Download, AlertTriangle, HardDrive, Key, Stethoscope, FileText, Settings } from 'lucide-react'

export function Dashboard() {
  const { namespace, health, loading, error, fetchData, startEventStream, stopEventStream } =
    useDashboardStore()
  const setHomeTab = useTabsStore((s) => s.setHomeTab)
  const { drawerAppName, closeDrawer, bottomTabs, openBottomTab } = usePanelStore()
  const { t } = useTranslation()

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
      <div className="flex h-full">
        {/* Skeleton left panel */}
        <div className="w-56 shrink-0 border-r border-border bg-card p-3 flex flex-col gap-3">
          <div className="h-4 w-32 bg-muted rounded animate-pulse" />
          <div className="h-3 w-24 bg-muted rounded animate-pulse" />
          <div className="h-6 w-20 bg-muted rounded animate-pulse" />
          <div className="h-px bg-border my-1" />
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i} className="h-3 w-full bg-muted rounded animate-pulse" />
          ))}
        </div>
        {/* Skeleton table */}
        <div className="flex-1 p-4 space-y-3">
          <div className="h-5 w-48 bg-muted rounded animate-pulse" />
          {Array.from({ length: 8 }).map((_, i) => (
            <div key={i} className="h-8 w-full bg-muted rounded animate-pulse" />
          ))}
        </div>
      </div>
    )
  }

  if (error && !namespace) {
    return <div className="text-destructive text-xs p-4">{t('dashboard.error', { error })}</div>
  }

  if (!namespace) return null

  const dockerCheck = health?.checks.find((c) => c.name === 'docker')
  const dockerError = dockerCheck?.status === 'error' ? dockerCheck.message : null
  const runningCount = namespace.apps.filter((a) => a.status === 'RUNNING').length
  const isRunning = namespace.status === 'RUNNING'
  const links = namespace.links ? [...namespace.links].sort((a, b) => a.order - b.order) : []
  const proxyUrl = links.find((l) => l.name === 'ECOS UI')?.url
  const serviceLinks = links.filter((l) => l.name !== 'ECOS UI')

  const runningApps = namespace.apps.filter((a) => a.status === 'RUNNING')
  const totalCpu = runningApps.reduce((sum, a) => sum + (parseFloat(a.cpu) || 0), 0)
  const totalMem = runningApps.reduce((sum, a) => {
    const m = a.memory?.split(' / ')[0]
    if (!m) return sum
    if (m.endsWith('G')) return sum + parseFloat(m) * 1024
    if (m.endsWith('M')) return sum + parseFloat(m)
    return sum
  }, 0)

  // Drawer title info
  const drawerApp = drawerAppName ? namespace.apps.find((a) => a.name === drawerAppName) : null

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
      default:
        return null
    }
  }

  return (
    <div className="flex flex-col h-full overflow-hidden">
      {/* Top: sidebar + table + drawer overlay */}
      <div className="flex flex-1 min-h-0 relative">
        {/* Left info panel */}
        <aside className="w-56 shrink-0 border-r border-border bg-card p-3 flex flex-col gap-2 overflow-y-auto">
          <div className="flex items-center justify-between">
            <div className="min-w-0">
              <div className="text-sm font-semibold truncate">{namespace.name}</div>
              <div className="text-[11px] text-muted-foreground mt-0.5 truncate">{namespace.bundleRef}</div>
            </div>
            <button
              type="button"
              className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted shrink-0"
              title={t('dashboard.nsConfig')}
              onClick={() => openBottomTab({ id: 'ns-config', type: 'ns-config', title: t('configEditor.title') })}
            >
              <Settings size={14} />
            </button>
          </div>

          <div className="flex items-center gap-2">
            <StatusBadge status={namespace.status} />
            <span className="text-xs text-muted-foreground">{runningCount}/{namespace.apps.length}</span>
          </div>

          {runningApps.length > 0 && (
            <div className="text-[11px] space-y-0.5">
              <div className="flex justify-between">
                <span className="text-muted-foreground">{t('dashboard.cpu')}</span>
                <span className="font-mono">{totalCpu.toFixed(1)}%</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">{t('dashboard.mem')}</span>
                <span className="font-mono">{totalMem >= 1024 ? `${(totalMem / 1024).toFixed(1)}G` : `${Math.round(totalMem)}M`}</span>
              </div>
            </div>
          )}

          <NamespaceControls status={namespace.status} />

          {proxyUrl && (
            <a
              href={proxyUrl}
              target="_blank"
              rel="noopener noreferrer"
              className={`flex items-center gap-1.5 rounded border px-2 py-1.5 text-xs ${
                isRunning
                  ? 'border-primary/40 text-primary hover:bg-primary/10'
                  : 'border-border text-muted-foreground cursor-not-allowed opacity-50'
              }`}
              onClick={(e) => { if (!isRunning) e.preventDefault() }}
              title={isRunning ? t('dashboard.openInBrowser.tooltip') : t('dashboard.openInBrowser.disabled')}
            >
              <Globe size={14} />
              {t('dashboard.openInBrowser')}
            </a>
          )}

          {dockerError && (
            <div className="rounded border border-destructive/30 bg-destructive/5 px-2 py-1.5 text-[11px] text-destructive">
              <AlertTriangle size={12} className="inline mr-1" />
              {t('dashboard.docker.error', { error: dockerError })}
              <button type="button" className="underline ml-1" onClick={fetchData}>{t('dashboard.docker.retry')}</button>
            </div>
          )}

          {serviceLinks.length > 0 && (
            <div>
              <div className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-1">{t('dashboard.links')}</div>
              <div className="flex flex-col gap-0.5">
                {serviceLinks.map((l) => (
                  <a key={l.name} href={l.url} target="_blank" rel="noopener noreferrer"
                    className={`flex items-center gap-1 text-xs py-0.5 ${
                      (isRunning || l.order >= 100) ? 'text-primary hover:underline' : 'text-muted-foreground cursor-not-allowed'
                    }`}
                    onClick={(e) => { if (!isRunning && l.order < 100) e.preventDefault() }}>
                    <ExternalLink size={11} />
                    {l.name}
                  </a>
                ))}
              </div>
            </div>
          )}

          <div className="mt-auto pt-2 border-t border-border flex flex-col gap-1">
            <SidebarBtn icon={HardDrive} label={t('dashboard.volumes')}
              onClick={() => { openTab({ id: 'volumes', title: t('dashboard.volumes'), path: '/volumes' }); navigate('/volumes') }} />
            <SidebarBtn icon={Key} label={t('dashboard.secrets')}
              onClick={() => { openTab({ id: 'secrets', title: t('dashboard.secrets'), path: '/secrets' }); navigate('/secrets') }} />
            <SidebarBtn icon={Stethoscope} label={t('dashboard.diagnostics')}
              onClick={() => { openTab({ id: 'diagnostics', title: t('dashboard.diagnostics'), path: '/diagnostics' }); navigate('/diagnostics') }} />
            <SidebarBtn icon={FileText} label={t('dashboard.launcherLogs')}
              onClick={() => openBottomTab({ id: 'daemon-logs', type: 'daemon-logs', title: t('daemonLogs.title') })} />
            <SidebarBtn icon={Download} label={t('dashboard.systemDump')}
              onClick={() => getSystemDump('zip').then(() => toast(t('dashboard.systemDump.success'), 'success')).catch((e) => toast((e as Error).message, 'error'))} />
          </div>
        </aside>

        {/* Main table area */}
        <div className="flex-1 min-w-0 p-2 overflow-auto">
          <AppTable apps={namespace.apps} highlightedApp={drawerAppName} />
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
    </div>
  )
}

function SidebarBtn({ icon: Icon, label, onClick }: { icon: React.ElementType; label: string; onClick?: () => void }) {
  return (
    <button type="button"
      className="flex items-center gap-1.5 text-xs py-1 px-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted"
      onClick={onClick} title={label}>
      <Icon size={13} />
      {label}
    </button>
  )
}
