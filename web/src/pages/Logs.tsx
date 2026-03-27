import { useEffect, useState, useRef, useCallback, useMemo } from 'react'
import { useParams, Link } from 'react-router'
import { getAppLogs } from '../lib/api'

type LogLevel = 'ERROR' | 'WARN' | 'INFO' | 'DEBUG' | 'TRACE'

const LOG_LEVELS: LogLevel[] = ['ERROR', 'WARN', 'INFO', 'DEBUG', 'TRACE']
const MAX_RENDER_LINES = 2000

// Kotlin-matching colors
const LEVEL_COLORS: Record<LogLevel, string> = {
  ERROR: 'text-[#ef5350]',
  WARN: 'text-[#ffa726]',
  INFO: 'text-[#66bb6a]',
  DEBUG: 'text-[#9e9e9e]',
  TRACE: 'text-[#bdbdbd]',
}

// 7 regex patterns matching Kotlin LogLevelDetector (ordered by confidence)
const LEVEL_PATTERNS: [RegExp, number][] = [
  [/\[(ERROR|WARN|INFO|DEBUG|TRACE)]/i, 1],                           // [ERROR]
  [/\|-(ERROR|WARN|INFO|DEBUG|TRACE)\b/i, 1],                         // |-ERROR (logback)
  [/\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\s+(ERROR|WARN|INFO|DEBUG|TRACE)\b/i, 1], // After timestamp
  [/\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[^\s]*\s+(ERROR|WARN|INFO|DEBUG|TRACE)\s/i, 1], // Spring Boot ISO
  [/^(ERROR|WARN|INFO|DEBUG|TRACE):/i, 1],                             // Python format
  [/\s(ERROR|WARN|INFO|DEBUG|TRACE)\s/i, 1],                           // Whitespace-surrounded
  [/^(ERROR|WARN|INFO|DEBUG|TRACE)[\s:\-[]/i, 1],                      // At line start
]

function detectLevel(line: string): LogLevel | null {
  for (const [pattern, group] of LEVEL_PATTERNS) {
    const m = line.match(pattern)
    if (m) return m[group].toUpperCase() as LogLevel
  }
  return null
}

// Apply level inheritance — lines without level inherit from previous (stack traces etc.)
function detectLevels(lines: string[]): (LogLevel | null)[] {
  const result: (LogLevel | null)[] = []
  let lastLevel: LogLevel | null = null
  for (const line of lines) {
    const level = detectLevel(line)
    if (level) {
      lastLevel = level
      result.push(level)
    } else {
      result.push(lastLevel) // inherit from previous
    }
  }
  return result
}

export function Logs() {
  const { name } = useParams<{ name: string }>()
  const [logs, setLogs] = useState('')
  const [tail, setTail] = useState(500)
  const [search, setSearch] = useState('')
  const [debouncedSearch, setDebouncedSearch] = useState('')
  const [useRegex, setUseRegex] = useState(false)
  const [follow, setFollow] = useState(true)
  const [wordWrap, setWordWrap] = useState(true)
  const [enabledLevels, setEnabledLevels] = useState<Set<LogLevel>>(new Set(LOG_LEVELS))
  const [error, setError] = useState<string | null>(null)
  const [matchIndex, setMatchIndex] = useState(0)
  const logRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)

  // Debounce search input (300ms) and limit pattern length
  useEffect(() => {
    const trimmed = search.slice(0, 200)
    const timer = setTimeout(() => setDebouncedSearch(trimmed), 300)
    return () => clearTimeout(timer)
  }, [search])

  const fetchLogs = useCallback(() => {
    if (!name) return
    getAppLogs(name, tail)
      .then((data) => {
        setLogs(data)
        setError(null)
      })
      .catch((e) => setError(e.message))
  }, [name, tail])

  useEffect(() => {
    fetchLogs()
  }, [fetchLogs])

  // Auto-refresh when follow is on
  useEffect(() => {
    if (!follow) return
    const interval = setInterval(fetchLogs, 2000)
    return () => clearInterval(interval)
  }, [follow, fetchLogs])

  // Scroll to bottom when following
  useEffect(() => {
    if (follow && logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight
    }
  }, [logs, follow])

  // Keyboard shortcuts
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if ((e.ctrlKey || e.metaKey) && e.key === 'f') {
        e.preventDefault()
        searchRef.current?.focus()
      }
      if (e.key === 'F3' || ((e.ctrlKey || e.metaKey) && e.key === 'g')) {
        e.preventDefault()
        setMatchIndex((prev) => prev + (e.shiftKey ? -1 : 1))
      }
      if ((e.ctrlKey || e.metaKey) && e.key === 'l') {
        e.preventDefault()
        setLogs('')
      }
      if (e.key === 'Escape') {
        setSearch('')
        searchRef.current?.blur()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [])

  // Detect levels with inheritance, then filter (memoized for performance)
  const { filteredLines, filteredLevels, totalLineCount } = useMemo(() => {
    const lines = logs.split('\n')
    const allLevels = detectLevels(lines)
    const entries = lines
      .map((line, i) => ({ line, level: allLevels[i] }))
      .filter(({ level }) => !level || enabledLevels.has(level))
    return {
      filteredLines: entries.map((e) => e.line),
      filteredLevels: entries.map((e) => e.level),
      totalLineCount: lines.length,
    }
  }, [logs, enabledLevels])

  const searchMatches = useMemo(() => {
    const matches = new Set<number>()
    if (debouncedSearch) {
      try {
        const pattern = useRegex ? new RegExp(debouncedSearch, 'i') : null
        filteredLines.forEach((line, i) => {
          if (pattern ? pattern.test(line) : line.toLowerCase().includes(debouncedSearch.toLowerCase())) {
            matches.add(i)
          }
        })
      } catch { /* invalid regex */ }
    }
    return matches
  }, [filteredLines, debouncedSearch, useRegex])

  const matchIndices = Array.from(searchMatches).sort((a, b) => a - b)
  const safeMatchIndex = matchIndices.length > 0
    ? ((matchIndex % matchIndices.length) + matchIndices.length) % matchIndices.length
    : 0

  // Reset match index when matches change (log refresh, filter change)
  useEffect(() => { setMatchIndex(0) }, [searchMatches])

  function toggleLevel(level: LogLevel) {
    setEnabledLevels((prev) => {
      const next = new Set(prev)
      if (next.has(level)) next.delete(level)
      else next.add(level)
      return next
    })
  }

  function copyToClipboard() {
    navigator.clipboard.writeText(filteredLines.join('\n'))
  }

  function downloadLogs() {
    const blob = new Blob([filteredLines.join('\n')], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${name}-logs.txt`
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 5000)
  }

  return (
    <div className="flex flex-col h-[calc(100vh-100px)]">
      {/* Header */}
      <div className="flex items-center justify-between mb-4">
        <div>
          <Link to={`/apps/${name}`} className="text-sm text-primary hover:underline">
            &larr; Back to {name}
          </Link>
          <h1 className="text-2xl font-semibold mt-1">Logs: {name}</h1>
        </div>
      </div>

      {/* Toolbar */}
      <div className="flex items-center gap-2 mb-3 flex-wrap">
        {/* Search */}
        <div className="flex items-center gap-1">
          <input
            ref={searchRef}
            type="text"
            value={search}
            onChange={(e) => { setSearch(e.target.value); setMatchIndex(0) }}
            placeholder="Search... (Ctrl+F)"
            className="w-56 rounded-md border border-border bg-card px-3 py-1.5 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-primary"
          />
          <button
            type="button"
            className={`rounded px-2 py-1.5 text-xs border ${useRegex ? 'border-primary bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:bg-muted'}`}
            onClick={() => setUseRegex(!useRegex)}
            title="Toggle regex"
          >
            .*
          </button>
          {matchIndices.length > 0 && (
            <>
              <button
                type="button"
                className="rounded px-1.5 py-1.5 text-xs border border-border text-muted-foreground hover:bg-muted"
                onClick={() => setMatchIndex((p) => p - 1)}
                title="Previous match (Shift+F3)"
              >
                &uarr;
              </button>
              <button
                type="button"
                className="rounded px-1.5 py-1.5 text-xs border border-border text-muted-foreground hover:bg-muted"
                onClick={() => setMatchIndex((p) => p + 1)}
                title="Next match (F3)"
              >
                &darr;
              </button>
              <span className="text-xs text-muted-foreground">
                {safeMatchIndex + 1}/{matchIndices.length}
              </span>
            </>
          )}
        </div>

        <div className="h-5 w-px bg-border" />

        {/* Level filters */}
        <div className="flex items-center gap-1">
          {LOG_LEVELS.map((level) => (
            <button
              key={level}
              type="button"
              className={`rounded px-2 py-1 text-xs font-medium ${
                enabledLevels.has(level)
                  ? `${LEVEL_COLORS[level]} bg-muted`
                  : 'text-muted-foreground/50 line-through'
              }`}
              onClick={() => toggleLevel(level)}
            >
              {level}
            </button>
          ))}
        </div>

        <div className="h-5 w-px bg-border" />

        {/* Tail lines */}
        <select
          value={tail}
          onChange={(e) => setTail(Number(e.target.value))}
          className="rounded-md border border-border bg-card px-2 py-1.5 text-xs text-foreground"
        >
          <option value={100}>100</option>
          <option value={200}>200</option>
          <option value={500}>500</option>
          <option value={1000}>1000</option>
          <option value={5000}>5000</option>
        </select>

        <div className="h-5 w-px bg-border" />

        {/* Toggles */}
        <button
          type="button"
          className={`rounded px-2 py-1.5 text-xs border ${follow ? 'border-primary bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:bg-muted'}`}
          onClick={() => setFollow(!follow)}
          title="Auto-scroll to bottom"
        >
          Follow
        </button>
        <button
          type="button"
          className={`rounded px-2 py-1.5 text-xs border ${wordWrap ? 'border-primary bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:bg-muted'}`}
          onClick={() => setWordWrap(!wordWrap)}
          title="Toggle word wrap"
        >
          Wrap
        </button>

        <div className="flex-1" />

        {/* Actions */}
        <button
          type="button"
          className="rounded px-2 py-1.5 text-xs border border-border text-muted-foreground hover:bg-muted"
          onClick={copyToClipboard}
          title="Copy all to clipboard"
        >
          Copy
        </button>
        <button
          type="button"
          className="rounded px-2 py-1.5 text-xs border border-border text-muted-foreground hover:bg-muted"
          onClick={downloadLogs}
          title="Download as file"
        >
          Download
        </button>
        <button
          type="button"
          className="rounded px-2 py-1.5 text-xs border border-border text-muted-foreground hover:bg-muted"
          onClick={() => setLogs('')}
          title="Clear logs (Ctrl+L)"
        >
          Clear
        </button>
        <button
          type="button"
          className="rounded px-2 py-1.5 text-xs border border-border text-muted-foreground hover:bg-muted"
          onClick={fetchLogs}
        >
          Refresh
        </button>
      </div>

      {error && <div className="text-destructive text-sm mb-2">Error: {error}</div>}

      {/* Log output */}
      <div
        ref={logRef}
        className={`flex-1 overflow-auto rounded-lg border border-border bg-card p-4 font-mono text-xs ${wordWrap ? 'whitespace-pre-wrap' : 'whitespace-pre'}`}
        onScroll={() => {
          if (!logRef.current) return
          const { scrollTop, scrollHeight, clientHeight } = logRef.current
          if (scrollHeight - scrollTop - clientHeight > 50) {
            setFollow(false)
          }
        }}
      >
        {filteredLines.length === 0 ? (
          <span className="text-muted-foreground">No logs available</span>
        ) : (
          <>
            {filteredLines.length > MAX_RENDER_LINES && (
              <div className="text-xs text-warning mb-1">Showing last {MAX_RENDER_LINES} of {filteredLines.length} lines</div>
            )}
            {filteredLines.slice(-MAX_RENDER_LINES).map((line, i) => {
              const realIdx = Math.max(0, filteredLines.length - MAX_RENDER_LINES) + i
              const level = filteredLevels[realIdx]
              const colorClass = level ? LEVEL_COLORS[level] : 'text-foreground'
              const isCurrentMatch = matchIndices[safeMatchIndex] === realIdx

              return (
                <div
                  key={realIdx}
                  className={`${colorClass} ${isCurrentMatch ? 'bg-primary/20' : searchMatches.has(realIdx) ? 'bg-primary/10' : ''}`}
                >
                  {search ? highlightSearch(line, search, useRegex) : line}
                </div>
              )
            })}
          </>
        )}
      </div>

      {/* Status bar */}
      <div className="mt-2 flex items-center justify-between text-xs text-muted-foreground">
        <span>
          {filteredLines.length} lines
          {filteredLines.length !== totalLineCount && ` (${totalLineCount} total)`}
        </span>
        <span>
          {follow && '⬇ Following'} | Ctrl+F search | F3 next | Shift+F3 prev | Esc clear
        </span>
      </div>
    </div>
  )
}

function highlightSearch(line: string, search: string, useRegex: boolean): React.ReactNode {
  try {
    const regex = useRegex ? new RegExp(`(${search})`, 'gi') : new RegExp(`(${escapeRegExp(search)})`, 'gi')
    const parts = line.split(regex)
    if (parts.length === 1) return line
    // After split with capturing group, odd indices are matches
    return parts.map((part, i) =>
      i % 2 === 1 ? (
        <mark key={i} className="bg-warning/40 text-inherit rounded-sm px-0.5">
          {part}
        </mark>
      ) : (
        part
      ),
    )
  } catch {
    return line
  }
}

function escapeRegExp(str: string): string {
  return str.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
