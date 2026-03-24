import { useEffect, useState, useRef } from 'react'
import { useParams, Link } from 'react-router'
import { getAppLogs } from '../lib/api'

export function Logs() {
  const { name } = useParams<{ name: string }>()
  const [logs, setLogs] = useState('')
  const [tail, setTail] = useState(200)
  const [search, setSearch] = useState('')
  const [autoRefresh, setAutoRefresh] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const logRef = useRef<HTMLPreElement>(null)

  const fetchLogs = () => {
    if (!name) return
    getAppLogs(name, tail)
      .then((data) => {
        setLogs(data)
        setError(null)
      })
      .catch((e) => setError(e.message))
  }

  useEffect(() => {
    fetchLogs()
  }, [name, tail])

  useEffect(() => {
    if (!autoRefresh) return
    const interval = setInterval(fetchLogs, 2000)
    return () => clearInterval(interval)
  }, [autoRefresh, name, tail])

  useEffect(() => {
    if (logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight
    }
  }, [logs])

  const filteredLogs = search
    ? logs
        .split('\n')
        .filter((line) => line.toLowerCase().includes(search.toLowerCase()))
        .join('\n')
    : logs

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <Link to={`/apps/${name}`} className="text-sm text-primary hover:underline">
            &larr; Back to {name}
          </Link>
          <h1 className="text-2xl font-semibold mt-2">Logs: {name}</h1>
        </div>
      </div>

      {/* Controls */}
      <div className="flex items-center gap-3 flex-wrap">
        <input
          type="text"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search logs..."
          className="rounded-md border border-border bg-card px-3 py-1.5 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-primary"
        />
        <select
          value={tail}
          onChange={(e) => setTail(Number(e.target.value))}
          className="rounded-md border border-border bg-card px-3 py-1.5 text-sm text-foreground"
        >
          <option value={50}>Last 50 lines</option>
          <option value={100}>Last 100 lines</option>
          <option value={200}>Last 200 lines</option>
          <option value={500}>Last 500 lines</option>
          <option value={1000}>Last 1000 lines</option>
        </select>
        <label className="flex items-center gap-2 text-sm text-muted-foreground">
          <input
            type="checkbox"
            checked={autoRefresh}
            onChange={(e) => setAutoRefresh(e.target.checked)}
            className="rounded"
          />
          Auto-refresh
        </label>
        <button
          onClick={fetchLogs}
          className="rounded-md border border-border bg-card px-3 py-1.5 text-sm hover:bg-muted"
        >
          Refresh
        </button>
      </div>

      {error && <div className="text-destructive text-sm">Error: {error}</div>}

      {/* Log output */}
      <pre
        ref={logRef}
        className="h-[calc(100vh-280px)] overflow-y-auto rounded-lg border border-border bg-card p-4 text-xs font-mono text-muted-foreground whitespace-pre-wrap"
      >
        {filteredLogs || 'No logs available'}
      </pre>
    </div>
  )
}
