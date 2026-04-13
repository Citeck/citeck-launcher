import { useEffect, useState, useCallback } from 'react'
import { useNavigate } from 'react-router'
import { useDashboardStore } from '../lib/store'
import { useTabsStore } from '../lib/tabs'
import { usePanelStore } from '../lib/panels'
import { getSystemDump, getMigrationStatus, submitMasterPassword, unlockSecrets, setupSecretsPassword, openExternal, getBundles, upgradeNamespace } from '../lib/api'
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
import { RestartEvents } from '../components/RestartEvents'
import { FormDialog } from '../components/FormDialog'
import type { BottomPanelTab } from '../lib/panels'
import { toast } from '../lib/toast'
import { ExternalLink, Globe, Download, AlertTriangle, HardDrive, Key, Stethoscope, FileText, Settings, Eye, EyeOff, ArrowUpCircle } from 'lucide-react'

export function Dashboard() {
  const { namespace, health, loading, error, fetchData, startEventStream, stopEventStream } =
    useDashboardStore()
  const setHomeTab = useTabsStore((s) => s.setHomeTab)
  const { drawerAppName, closeDrawer, bottomTabs, openBottomTab } = usePanelStore()
  const { t } = useTranslation()

  // Secret management dialog (Kotlin migration, setup, unlock)
  // Server mode: daemon auto-encrypts/auto-unlocks with default password, so
  // setup-password and unlock dialogs are never triggered. Only kotlin-decrypt
  // needs user input (original Kotlin master password).
  // Desktop mode: all three dialogs can appear.
  const [dialogStep, setDialogStep] = useState<'kotlin-decrypt' | 'setup-password' | 'unlock' | null>(null)
  const [password, setPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [dialogError, setDialogError] = useState('')
  const [dialogLoading, setDialogLoading] = useState(false)
  const [dialogChecked, setDialogChecked] = useState(false)
  const [showPassword, setShowPassword] = useState(false)

  // Upgrade dialog state
  const [upgradeOpen, setUpgradeOpen] = useState(false)
  const [upgradeVersions, setUpgradeVersions] = useState<{ label: string; value: string }[]>([])
  const [upgradeLoading, setUpgradeLoading] = useState(false)
  const [upgradeError, setUpgradeError] = useState<string | null>(null)

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
    if (dialogChecked || dialogStep) return
    if (!namespace) return
    setDialogChecked(true)
    getMigrationStatus().then((s) => {
      if (s.hasPendingSecrets) setDialogStep('kotlin-decrypt')
      else if (s.encrypted && s.locked) setDialogStep('unlock')
      else if (!s.encrypted && s.hasSecrets) setDialogStep('setup-password')
    }).catch(() => {})
  }, [namespace, dialogChecked, dialogStep])

  // Kotlin decrypt → import + encrypt secrets in one step
  const handleKotlinDecrypt = useCallback(async () => {
    if (!password) return
    setDialogLoading(true)
    setDialogError('')
    try {
      await submitMasterPassword(password)
      toast(t('migration.secretsImported'), 'success')
      setDialogStep(null)
      setPassword('')
      fetchData()
    } catch {
      setDialogError(t('migration.wrongPassword'))
    } finally {
      setDialogLoading(false)
    }
  }, [password, fetchData, t])

  // Setup password — encrypt all secrets (desktop mode only)
  const handleSetupPassword = useCallback(async (pwd: string) => {
    setDialogLoading(true)
    setDialogError('')
    try {
      await setupSecretsPassword(pwd)
      toast(t('migration.setupPassword.success'), 'success')
      setDialogStep(null)
      setPassword('')
      setNewPassword('')
      fetchData()
    } catch (e) {
      setDialogError((e as Error).message)
    } finally {
      setDialogLoading(false)
    }
  }, [fetchData, t])

  // Unlock encrypted secrets (desktop mode only)
  const handleUnlock = useCallback(async () => {
    if (!password) return
    setDialogLoading(true)
    setDialogError('')
    try {
      await unlockSecrets(password)
      toast(t('migration.unlock.success'), 'success')
      setDialogStep(null)
      setPassword('')
      fetchData()
    } catch {
      setDialogError(t('migration.wrongPassword'))
    } finally {
      setDialogLoading(false)
    }
  }, [password, fetchData, t])

  const handleSkipDialog = useCallback(() => {
    setDialogStep(null)
    setPassword('')
    setNewPassword('')
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
      {/* Multi-step dialog: kotlin-decrypt / setup-password / unlock */}
      {dialogStep && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm">
          <div className="bg-card border border-border rounded-lg p-6 w-96 shadow-xl">
            {dialogStep === 'kotlin-decrypt' && (<>
              <h2 className="text-lg font-semibold mb-2">{t('migration.title')}</h2>
              <p className="text-sm text-muted-foreground mb-4">{t('migration.description')}</p>
              <div className="relative mb-2">
                <input
                  type={showPassword ? 'text' : 'password'}
                  className="w-full px-3 py-2 pr-10 bg-background border border-border rounded text-foreground"
                  placeholder={t('migration.passwordPlaceholder')}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && handleKotlinDecrypt()}
                  autoFocus
                />
                <button type="button" className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                  onClick={() => setShowPassword(!showPassword)} tabIndex={-1}>
                  {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                </button>
              </div>
              {dialogError && <p className="text-destructive text-sm mb-2">{dialogError}</p>}
              <div className="flex justify-between mt-4">
                <button type="button" className="text-sm text-muted-foreground hover:text-foreground" onClick={handleSkipDialog}>
                  {t('migration.skip')}
                </button>
                <button
                  type="button"
                  className="px-4 py-1.5 bg-primary text-primary-foreground rounded text-sm font-medium disabled:opacity-50"
                  onClick={handleKotlinDecrypt}
                  disabled={dialogLoading || !password}
                >
                  {dialogLoading ? '...' : t('migration.confirm')}
                </button>
              </div>
            </>)}

            {dialogStep === 'setup-password' && (<>
              <h2 className="text-lg font-semibold mb-2">{t('migration.setupPassword.title')}</h2>
              <p className="text-sm text-muted-foreground mb-4">{t('migration.setupPassword.description')}</p>
              <div className="relative mb-2">
                <input
                  type={showPassword ? 'text' : 'password'}
                  className="w-full px-3 py-2 pr-10 bg-background border border-border rounded text-foreground"
                  placeholder={t('migration.passwordPlaceholder')}
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && newPassword && handleSetupPassword(newPassword)}
                  autoFocus
                />
                <button type="button" className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                  onClick={() => setShowPassword(!showPassword)} tabIndex={-1}>
                  {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                </button>
              </div>
              {dialogError && <p className="text-destructive text-sm mb-2">{dialogError}</p>}
              <div className="flex justify-end mt-4">
                <button
                  type="button"
                  className="px-4 py-1.5 bg-primary text-primary-foreground rounded text-sm font-medium disabled:opacity-50"
                  onClick={() => handleSetupPassword(newPassword)}
                  disabled={dialogLoading || !newPassword}
                >
                  {dialogLoading ? '...' : t('migration.confirm')}
                </button>
              </div>
            </>)}

            {dialogStep === 'unlock' && (<>
              <h2 className="text-lg font-semibold mb-2">{t('migration.unlock.title')}</h2>
              <p className="text-sm text-muted-foreground mb-4">{t('migration.unlock.description')}</p>
              <div className="relative mb-2">
                <input
                  type={showPassword ? 'text' : 'password'}
                  className="w-full px-3 py-2 pr-10 bg-background border border-border rounded text-foreground"
                  placeholder={t('migration.passwordPlaceholder')}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && handleUnlock()}
                  autoFocus
                />
                <button type="button" className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                  onClick={() => setShowPassword(!showPassword)} tabIndex={-1}>
                  {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                </button>
              </div>
              {dialogError && <p className="text-destructive text-sm mb-2">{dialogError}</p>}
              <div className="flex justify-between mt-4">
                <button type="button" className="text-sm text-muted-foreground hover:text-foreground" onClick={handleSkipDialog}>
                  {t('migration.skip')}
                </button>
                <button
                  type="button"
                  className="px-4 py-1.5 bg-primary text-primary-foreground rounded text-sm font-medium disabled:opacity-50"
                  onClick={handleUnlock}
                  disabled={dialogLoading || !password}
                >
                  {dialogLoading ? '...' : t('migration.unlock.confirm')}
                </button>
              </div>
            </>)}

          </div>
        </div>
      )}

      {/* Top: sidebar + table + drawer overlay */}
      <div className="flex flex-1 min-h-0 relative">
        {/* Left info panel */}
        <aside className="w-60 shrink-0 border-r border-border bg-card flex flex-col h-full">
          {/* Scrollable content */}
          <div className="flex-1 min-h-0 overflow-y-auto p-3 flex flex-col gap-2">
          <div className="flex items-center justify-between">
            <div className="min-w-0">
              <div className="text-sm font-semibold truncate">{namespace.name}</div>
              <div className="text-[11px] text-muted-foreground mt-0.5 truncate">{namespace.bundleRef}</div>
            </div>
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
                onClick={() => openBottomTab({ id: 'ns-config', type: 'ns-config', title: t('configEditor.title') })}
              >
                <Settings size={14} />
              </button>
            </div>
          </div>

          <div className="flex items-center gap-2">
            <StatusBadge status={namespace.status} />
            <span className="text-xs text-muted-foreground">{runningCount}/{apps.length}</span>
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
            <button
              type="button"
              className={`flex items-center gap-1.5 rounded border px-2 py-1.5 text-xs ${
                isRunning
                  ? 'border-primary/40 text-primary hover:bg-primary/10 cursor-pointer'
                  : 'border-border text-muted-foreground cursor-not-allowed opacity-50'
              }`}
              onClick={() => { if (isRunning) openExternal(proxyUrl) }}
              title={isRunning ? t('dashboard.openInBrowser.tooltip') : t('dashboard.openInBrowser.disabled')}
            >
              <Globe size={14} />
              {t('dashboard.openInBrowser')}
            </button>
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
                    onClick={(e) => {
                      e.preventDefault()
                      if (!isRunning && l.order < 100) return
                      openExternal(l.url)
                    }}>
                    <ExternalLink size={11} />
                    {l.name}
                  </a>
                ))}
              </div>
            </div>
          )}

          </div>
          {/* Fixed footer — always visible at bottom */}
          <div className="shrink-0 p-3 pt-2 border-t border-border flex flex-col gap-1">
            <SidebarBtn icon={HardDrive} label={t('dashboard.volumes')}
              onClick={() => { openTab({ id: 'volumes', title: t('dashboard.volumes'), path: '/volumes' }); navigate('/volumes') }} />
            <SidebarBtn icon={Key} label={t('dashboard.secrets')}
              onClick={() => { openTab({ id: 'secrets', title: t('dashboard.secrets'), path: '/secrets' }); navigate('/secrets') }} />
            <SidebarBtn icon={Stethoscope} label={t('dashboard.diagnostics')}
              onClick={() => { openTab({ id: 'diagnostics', title: t('dashboard.diagnostics'), path: '/diagnostics' }); navigate('/diagnostics') }} />
            <SidebarBtn icon={AlertTriangle} label={t('dashboard.restartEvents')}
              onClick={() => openBottomTab({ id: 'restart-events', type: 'restart-events', title: t('dashboard.restartEvents') })} />
            <SidebarBtn icon={FileText} label={t('dashboard.launcherLogs')}
              onClick={() => openBottomTab({ id: 'daemon-logs', type: 'daemon-logs', title: t('daemonLogs.title') })} />
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
