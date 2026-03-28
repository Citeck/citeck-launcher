import { useEffect, useState, useCallback, useRef } from 'react'
import { getDaemonLogs } from '../lib/api'

interface DaemonLogsViewerProps {
  compact?: boolean
  active?: boolean
}

export function DaemonLogsViewer({ compact = false, active = true }: DaemonLogsViewerProps) {
  const [logs, setLogs] = useState('')
  const [error, setError] = useState<string | null>(null)
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const fetchLogs = useCallback(() => {
    getDaemonLogs(500)
      .then((data) => { setLogs(data); setError(null) })
      .catch((e) => setError(e.message))
  }, [])

  useEffect(() => {
    if (!active) {
      if (intervalRef.current) {
        clearInterval(intervalRef.current)
        intervalRef.current = null
      }
      return
    }

    fetchLogs()

    const startPolling = () => {
      if (!intervalRef.current) {
        intervalRef.current = setInterval(fetchLogs, 5000)
      }
    }
    const stopPolling = () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current)
        intervalRef.current = null
      }
    }

    startPolling()

    const handleVisibility = () => {
      if (document.hidden) {
        stopPolling()
      } else {
        fetchLogs()
        startPolling()
      }
    }
    document.addEventListener('visibilitychange', handleVisibility)

    return () => {
      stopPolling()
      document.removeEventListener('visibilitychange', handleVisibility)
    }
  }, [fetchLogs, active])

  return (
    <div className={compact ? 'flex flex-col h-full px-2 py-1' : 'p-3 flex flex-col h-full'}>
      <div className="flex items-center justify-between mb-1 shrink-0">
        <h2 className={compact ? 'text-xs font-medium' : 'text-base font-semibold'}>Daemon Logs</h2>
        <button type="button" className="rounded border border-border px-2 py-0.5 text-xs hover:bg-muted"
          onClick={fetchLogs}>Refresh</button>
      </div>
      {error && <div className="text-destructive text-xs mb-1 shrink-0">{error}</div>}
      <pre className="flex-1 min-h-0 overflow-auto rounded border border-border bg-background p-2 text-[11px] font-mono text-foreground whitespace-pre-wrap">
        {logs || 'No logs available'}
      </pre>
    </div>
  )
}
