import { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router'
import { getAppInspect, getAppLogs, postAppRestart } from '../lib/api'
import type { AppInspectDto } from '../lib/types'
import { StatusBadge } from '../components/StatusBadge'

function formatUptime(ms: number): string {
  if (ms <= 0) return '—'
  const seconds = Math.floor(ms / 1000)
  const minutes = Math.floor(seconds / 60)
  const hours = Math.floor(minutes / 60)
  const days = Math.floor(hours / 24)
  if (days > 0) return `${days}d ${hours % 24}h ${minutes % 60}m`
  if (hours > 0) return `${hours}h ${minutes % 60}m ${seconds % 60}s`
  if (minutes > 0) return `${minutes}m ${seconds % 60}s`
  return `${seconds}s`
}

export function AppDetail() {
  const { name } = useParams<{ name: string }>()
  const [inspect, setInspect] = useState<AppInspectDto | null>(null)
  const [logs, setLogs] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [restarting, setRestarting] = useState(false)

  useEffect(() => {
    if (!name) return
    getAppInspect(name).then(setInspect).catch((e) => setError(e.message))
    getAppLogs(name, 50).then(setLogs).catch(() => {})
  }, [name])

  const handleRestart = async () => {
    if (!name) return
    setRestarting(true)
    try {
      await postAppRestart(name)
    } finally {
      setRestarting(false)
    }
  }

  if (error) {
    return (
      <div className="space-y-4">
        <Link to="/" className="text-sm text-primary hover:underline">
          &larr; Back to dashboard
        </Link>
        <div className="text-destructive">Error: {error}</div>
      </div>
    )
  }

  if (!inspect) {
    return <div className="text-muted-foreground">Loading...</div>
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <Link to="/" className="text-sm text-primary hover:underline">
            &larr; Back to dashboard
          </Link>
          <h1 className="text-2xl font-semibold mt-2">{inspect.name}</h1>
        </div>
        <div className="flex items-center gap-3">
          <StatusBadge status={inspect.status} />
          <button
            onClick={handleRestart}
            disabled={restarting}
            className="rounded-md bg-primary px-3 py-1.5 text-sm text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          >
            {restarting ? 'Restarting...' : 'Restart'}
          </button>
        </div>
      </div>

      {/* Details */}
      <div className="rounded-lg border border-border bg-card p-4 space-y-3">
        <h2 className="text-lg font-medium">Details</h2>
        <div className="grid grid-cols-2 gap-2 text-sm">
          <Detail label="Container ID" value={inspect.containerId?.slice(0, 12) || '—'} />
          <Detail label="Image" value={inspect.image} />
          <Detail label="State" value={inspect.state} />
          <Detail label="Network" value={inspect.network} />
          <Detail label="Started" value={inspect.startedAt ? new Date(inspect.startedAt).toLocaleString() : '—'} />
          <Detail label="Uptime" value={formatUptime(inspect.uptime)} />
          <Detail label="Restarts" value={String(inspect.restartCount)} />
        </div>
      </div>

      {/* Ports */}
      {inspect.ports?.length > 0 && (
        <div className="rounded-lg border border-border bg-card p-4 space-y-2">
          <h2 className="text-lg font-medium">Ports</h2>
          <div className="flex flex-wrap gap-2">
            {inspect.ports.map((p, i) => (
              <span key={i} className="rounded bg-muted px-2 py-1 text-xs font-mono">
                {p}
              </span>
            ))}
          </div>
        </div>
      )}

      {/* Volumes */}
      {inspect.volumes?.length > 0 && (
        <div className="rounded-lg border border-border bg-card p-4 space-y-2">
          <h2 className="text-lg font-medium">Volumes</h2>
          {inspect.volumes.map((v, i) => (
            <div key={i} className="text-xs font-mono text-muted-foreground break-all">
              {v}
            </div>
          ))}
        </div>
      )}

      {/* Environment */}
      {inspect.env?.length > 0 && (
        <div className="rounded-lg border border-border bg-card p-4 space-y-2">
          <h2 className="text-lg font-medium">Environment</h2>
          <div className="max-h-64 overflow-y-auto">
            {inspect.env.map((e, i) => (
              <div key={i} className="text-xs font-mono text-muted-foreground break-all">
                {e}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Logs preview */}
      {logs && (
        <div className="rounded-lg border border-border bg-card p-4 space-y-2">
          <div className="flex items-center justify-between">
            <h2 className="text-lg font-medium">Recent Logs</h2>
            <Link to={`/apps/${name}/logs`} className="text-sm text-primary hover:underline">
              View full logs &rarr;
            </Link>
          </div>
          <pre className="max-h-48 overflow-y-auto rounded bg-background p-3 text-xs font-mono text-muted-foreground whitespace-pre-wrap">
            {logs}
          </pre>
        </div>
      )}
    </div>
  )
}

function Detail({ label, value }: { label: string; value: string }) {
  return (
    <>
      <span className="text-muted-foreground">{label}</span>
      <span className="font-mono">{value}</span>
    </>
  )
}
