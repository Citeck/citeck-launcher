import { useEffect } from 'react'
import { useDashboardStore } from '../lib/store'
import { StatusBadge } from '../components/StatusBadge'
import { AppTable } from '../components/AppTable'

export function Dashboard() {
  const { namespace, health, loading, error, fetchData, startEventStream, stopEventStream } =
    useDashboardStore()

  useEffect(() => {
    fetchData()
    startEventStream()
    return () => stopEventStream()
  }, [fetchData, startEventStream, stopEventStream])

  if (loading && !namespace) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-muted-foreground">Loading...</div>
      </div>
    )
  }

  if (error && !namespace) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-destructive">Error: {error}</div>
      </div>
    )
  }

  if (!namespace) return null

  const runningCount = namespace.apps.filter((a) => a.status === 'RUNNING').length
  const totalCount = namespace.apps.length

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{namespace.name}</h1>
          <div className="mt-1 flex items-center gap-3 text-sm text-muted-foreground">
            {namespace.bundleRef && <span>Bundle: {namespace.bundleRef}</span>}
            <span>
              {runningCount}/{totalCount} apps running
            </span>
          </div>
        </div>
        <StatusBadge status={namespace.status} />
      </div>

      {/* Health indicator */}
      {health && (
        <div
          className={`rounded-lg border px-4 py-3 text-sm ${
            health.healthy
              ? 'border-success/30 bg-success/5 text-success'
              : 'border-destructive/30 bg-destructive/5 text-destructive'
          }`}
        >
          System: {health.healthy ? 'Healthy' : 'Unhealthy'}
        </div>
      )}

      {/* App table */}
      <div className="rounded-lg border border-border bg-card p-4">
        <AppTable apps={namespace.apps} />
      </div>
    </div>
  )
}
