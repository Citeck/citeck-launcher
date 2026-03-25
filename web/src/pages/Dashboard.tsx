import { useEffect } from 'react'
import { useDashboardStore } from '../lib/store'
import { StatusBadge } from '../components/StatusBadge'
import { AppTable } from '../components/AppTable'
import { NamespaceControls } from '../components/NamespaceControls'
import { ExternalLink } from 'lucide-react'

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
  const links = namespace.links ? [...namespace.links].sort((a, b) => a.order - b.order) : []

  return (
    <div className="flex h-screen">
      {/* Left info panel */}
      <div className="w-52 shrink-0 border-r border-border bg-card p-3 flex flex-col gap-3 overflow-y-auto">
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

        {/* Docker error */}
        {dockerError && (
          <div className="rounded border border-destructive/30 bg-destructive/5 px-2 py-1.5 text-[11px] text-destructive">
            Docker: {dockerError}
            <button type="button" className="underline ml-1" onClick={fetchData}>Retry</button>
          </div>
        )}

        {/* Quick links */}
        {links.length > 0 && (
          <div>
            <div className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-1">Links</div>
            <div className="flex flex-col gap-0.5">
              {links.map((l) => (
                <a
                  key={l.name}
                  href={l.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="flex items-center gap-1 text-xs text-primary hover:underline py-0.5"
                >
                  <ExternalLink size={11} />
                  {l.name}
                </a>
              ))}
            </div>
          </div>
        )}
      </div>

      {/* Main table area */}
      <div className="flex-1 min-w-0 p-2 overflow-auto">
        <AppTable apps={namespace.apps} />
      </div>
    </div>
  )
}
