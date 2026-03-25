import { useEffect } from 'react'
import { useDashboardStore } from '../lib/store'
import { StatusBadge } from '../components/StatusBadge'
import { AppTable } from '../components/AppTable'
import { NamespaceControls } from '../components/NamespaceControls'
import { ExternalLink, Globe, FileText, AlertTriangle } from 'lucide-react'

export function Dashboard() {
  const { namespace, health, loading, error, fetchData, startEventStream, stopEventStream } =
    useDashboardStore()

  useEffect(() => {
    fetchData()
    startEventStream()
    return () => stopEventStream()
  }, [fetchData, startEventStream, stopEventStream])

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

  return (
    <div className="flex h-full">
      {/* Left info panel */}
      <div className="w-52 shrink-0 border-r border-border bg-card p-3 flex flex-col gap-2.5 overflow-y-auto">
        {/* Namespace info */}
        <div>
          <div className="text-sm font-semibold">{namespace.name}</div>
          <div className="text-[11px] text-muted-foreground mt-0.5">{namespace.bundleRef}</div>
        </div>

        {/* Status */}
        <div className="flex items-center gap-2">
          <StatusBadge status={namespace.status} />
          <span className="text-xs text-muted-foreground">{runningCount}/{namespace.apps.length}</span>
        </div>

        {/* Controls */}
        <NamespaceControls status={namespace.status} />

        {/* Open In Browser */}
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
            title={isRunning
              ? 'Open Citeck in browser.\nDefault: admin / admin'
              : 'Start the namespace first'}
          >
            <Globe size={14} />
            Open In Browser
          </a>
        )}

        {/* Docker error */}
        {dockerError && (
          <div className="rounded border border-destructive/30 bg-destructive/5 px-2 py-1.5 text-[11px] text-destructive">
            <AlertTriangle size={12} className="inline mr-1" />
            Docker: {dockerError}
            <button type="button" className="underline ml-1" onClick={fetchData}>Retry</button>
          </div>
        )}

        {/* Links */}
        {serviceLinks.length > 0 && (
          <div>
            <div className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-1">Links</div>
            <div className="flex flex-col gap-0.5">
              {serviceLinks.map((l) => (
                <a
                  key={l.name}
                  href={l.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className={`flex items-center gap-1 text-xs py-0.5 ${
                    (isRunning || l.order >= 100)
                      ? 'text-primary hover:underline'
                      : 'text-muted-foreground cursor-not-allowed'
                  }`}
                  onClick={(e) => { if (!isRunning && l.order < 100) e.preventDefault() }}
                >
                  <ExternalLink size={11} />
                  {l.name}
                </a>
              ))}
            </div>
          </div>
        )}

        {/* Bottom actions */}
        <div className="mt-auto pt-2 border-t border-border flex flex-col gap-1">
          <SidebarBtn icon={FileText} label="Launcher Logs" disabled />
          <SidebarBtn icon={AlertTriangle} label="System Info" disabled />
        </div>
      </div>

      {/* Main table area */}
      <div className="flex-1 min-w-0 p-2 overflow-auto">
        <AppTable apps={namespace.apps} />
      </div>
    </div>
  )
}

function SidebarBtn({ icon: Icon, label, disabled }: { icon: React.ElementType; label: string; disabled?: boolean }) {
  return (
    <button
      type="button"
      className={`flex items-center gap-1.5 text-xs py-1 px-1 rounded ${
        disabled ? 'text-muted-foreground/50 cursor-not-allowed' : 'text-muted-foreground hover:text-foreground hover:bg-muted'
      }`}
      disabled={disabled}
      title={disabled ? `${label} (not implemented yet)` : label}
    >
      <Icon size={13} />
      {label}
    </button>
  )
}
