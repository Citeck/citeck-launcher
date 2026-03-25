import { useEffect, useState, useCallback } from 'react'
import { getDaemonLogs } from '../lib/api'

export function DaemonLogs() {
  const [logs, setLogs] = useState('')
  const [error, setError] = useState<string | null>(null)

  const fetchLogs = useCallback(() => {
    getDaemonLogs(500).then(setLogs).catch((e) => setError(e.message))
  }, [])

  useEffect(() => {
    fetchLogs()
    const interval = setInterval(fetchLogs, 3000)
    return () => clearInterval(interval)
  }, [fetchLogs])

  return (
    <div className="p-3 flex flex-col h-full">
      <div className="flex items-center justify-between mb-2">
        <h1 className="text-base font-semibold">Daemon Logs</h1>
        <button type="button" className="rounded border border-border px-2 py-1 text-xs hover:bg-muted"
          onClick={fetchLogs}>Refresh</button>
      </div>
      {error && <div className="text-destructive text-xs mb-2">{error}</div>}
      <pre className="flex-1 overflow-auto rounded border border-border bg-background p-2 text-[11px] font-mono text-foreground whitespace-pre-wrap">
        {logs || 'No logs available'}
      </pre>
    </div>
  )
}
