import { useEffect } from 'react'
import { useNavigate } from 'react-router'
import { useDashboardStore } from '../lib/store'
import { useTabsStore } from '../lib/tabs'
import { getSystemDump } from '../lib/api'
import { StatusBadge } from '../components/StatusBadge'
import { AppTable } from '../components/AppTable'
import { NamespaceControls } from '../components/NamespaceControls'
import { ExternalLink, Globe, FileText, Download, AlertTriangle, HardDrive } from 'lucide-react'

export function Dashboard() {
  const { namespace, health, loading, error, fetchData, startEventStream, stopEventStream } =
    useDashboardStore()

  useEffect(() => {
    fetchData()
    startEventStream()
    return () => stopEventStream()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- store methods are stable
  }, [])

  const navigate = useNavigate()
  const openTab = useTabsStore((s) => s.openTab)

  if (loading && !namespace) {
    return <div className="text-muted-foreground text-xs p-4">Loading...</div>
  }

  if (error && !namespace) {
    return <div className="text-destructive text-xs p-4">Error: {error}</div>
  }

  if (!namespace) return null

  const dockerCheck = health?.checks.find((c) => c.name === 'docker')
  const dockerError = dockerCheck?.status === 'error' ? dockerCheck.message : null
  const runningCount = namespace.apps.filter((a) => a.status === 'RUNNING').length
  const isRunning = namespace.status === 'RUNNING'
  const links = namespace.links ? [...namespace.links].sort((a, b) => a.order - b.order) : []
  const proxyUrl = links.find((l) => l.name === 'ECOS UI')?.url
  const serviceLinks = links.filter((l) => l.name !== 'ECOS UI')

  // Namespace stats summary
  const runningApps = namespace.apps.filter((a) => a.status === 'RUNNING')
  const totalCpu = runningApps.reduce((sum, a) => sum + (parseFloat(a.cpu) || 0), 0)
  const totalMem = runningApps.reduce((sum, a) => {
    const m = a.memory?.split(' / ')[0]
    if (!m) return sum
    if (m.endsWith('G')) return sum + parseFloat(m) * 1024
    if (m.endsWith('M')) return sum + parseFloat(m)
    return sum
  }, 0)

  return (
    <div className="flex h-full">
      {/* Left info panel */}
      <div className="w-52 shrink-0 border-r border-border bg-card p-3 flex flex-col gap-2 overflow-y-auto">
        <div>
          <div className="text-sm font-semibold">{namespace.name}</div>
          <div className="text-[11px] text-muted-foreground mt-0.5">{namespace.bundleRef}</div>
        </div>

        <div className="flex items-center gap-2">
          <StatusBadge status={namespace.status} />
          <span className="text-xs text-muted-foreground">{runningCount}/{namespace.apps.length}</span>
        </div>

        {/* Namespace stats */}
        {runningApps.length > 0 && (
          <div className="text-[11px] space-y-0.5">
            <div className="flex justify-between">
              <span className="text-muted-foreground">CPU</span>
              <span className="font-mono">{totalCpu.toFixed(1)}%</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">MEM</span>
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
            title={isRunning ? 'Open Citeck in browser\nDefault: admin / admin' : 'Start namespace first'}
          >
            <Globe size={14} />
            Open In Browser
          </a>
        )}

        {dockerError && (
          <div className="rounded border border-destructive/30 bg-destructive/5 px-2 py-1.5 text-[11px] text-destructive">
            <AlertTriangle size={12} className="inline mr-1" />
            Docker: {dockerError}
            <button type="button" className="underline ml-1" onClick={fetchData}>Retry</button>
          </div>
        )}

        {serviceLinks.length > 0 && (
          <div>
            <div className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-1">Links</div>
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
          <SidebarBtn icon={HardDrive} label="Volumes"
            onClick={() => { openTab({ id: 'volumes', title: 'Volumes', path: '/volumes' }); navigate('/volumes') }} />
          <SidebarBtn icon={FileText} label="Launcher Logs"
            onClick={() => { openTab({ id: 'daemon-logs', title: 'Daemon Logs', path: '/daemon-logs' }); navigate('/daemon-logs') }} />
          <SidebarBtn icon={Download} label="System Dump"
            onClick={() => getSystemDump().catch((e) => console.error('Dump failed:', e))} />
        </div>
      </div>

      <div className="flex-1 min-w-0 p-2 overflow-auto">
        <AppTable apps={namespace.apps} />
      </div>
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
