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

  // Check Docker availability from health checks
  const dockerCheck = health?.checks.find((c) => c.name === 'docker')
  const dockerError = dockerCheck?.status === 'error' ? dockerCheck.message : null

  return (
    <div className="space-y-6">
      {/* Docker error banner */}
      {dockerError && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/5 px-4 py-3 text-sm text-destructive">
          <span className="font-medium">Docker unavailable:</span> {dockerError}
          <p className="mt-1 text-xs text-destructive/70">
            Make sure Docker is installed and running.{' '}
            <a
              href="https://docs.docker.com/get-docker/"
              target="_blank"
              rel="noopener noreferrer"
              className="underline"
            >
              Install Docker
            </a>
          </p>
          <button
            type="button"
            className="mt-2 rounded-md border border-destructive/30 px-3 py-1 text-xs hover:bg-destructive/10"
            onClick={fetchData}
          >
            Retry
          </button>
        </div>
      )}

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
        <div className="flex items-center gap-3">
          <NamespaceControls status={namespace.status} />
          <StatusBadge status={namespace.status} />
        </div>
      </div>

      {/* Health indicator */}
      {health && !dockerError && (
        <div
          className={`rounded-lg border px-4 py-3 text-sm ${
            health.healthy
              ? 'border-success/30 bg-success/5 text-success'
              : 'border-destructive/30 bg-destructive/5 text-destructive'
          }`}
        >
          System: {health.healthy ? 'Healthy' : 'Unhealthy'}
          {namespace.status === 'RUNNING' && (
            <a
              href="/"
              target="_blank"
              rel="noopener noreferrer"
              className="ml-4 text-xs underline opacity-70 hover:opacity-100"
            >
              Open in Browser
            </a>
          )}
        </div>
      )}

      {/* App table */}
      <div className="rounded-lg border border-border bg-card p-4">
        <AppTable apps={namespace.apps} />
      </div>
    </div>
  )
}
