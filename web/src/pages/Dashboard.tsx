import { useEffect } from 'react'
import { useDashboardStore } from '../lib/store'
import { StatusBadge } from '../components/StatusBadge'
import { AppTable } from '../components/AppTable'
import { NamespaceControls } from '../components/NamespaceControls'

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

  return (
    <div>
      {/* Docker error */}
      {dockerError && (
        <div className="rounded border border-destructive/30 bg-destructive/5 px-3 py-1.5 text-xs text-destructive mb-2">
          Docker unavailable: {dockerError}
          {' '}<a href="https://docs.docker.com/get-docker/" target="_blank" rel="noopener noreferrer" className="underline">Install</a>
          {' '}<button type="button" className="underline" onClick={fetchData}>Retry</button>
        </div>
      )}

      {/* Header line */}
      <div className="flex items-center justify-between mb-2">
        <div className="flex items-center gap-2">
          <span className="text-sm font-semibold">{namespace.name}</span>
          <StatusBadge status={namespace.status} />
          <span className="text-xs text-muted-foreground">{namespace.bundleRef}</span>
          <span className="text-xs text-muted-foreground">·</span>
          <span className="text-xs text-muted-foreground">{runningCount}/{namespace.apps.length} running</span>
        </div>
        <div className="flex items-center gap-2">
          {namespace.links && namespace.links.length > 0 && (
            <div className="flex items-center gap-2 text-xs">
              {[...namespace.links].sort((a, b) => a.order - b.order).map((l) => (
                <a key={l.name} href={l.url} target="_blank" rel="noopener noreferrer" className="text-primary hover:underline">{l.name}</a>
              ))}
              <span className="text-border">|</span>
            </div>
          )}
          <NamespaceControls status={namespace.status} />
        </div>
      </div>

      {/* App table — no wrapper card, maximum density */}
      <AppTable apps={namespace.apps} />
    </div>
  )
}
