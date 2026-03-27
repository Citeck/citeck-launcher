import { useEffect, useState, useRef, useCallback, useMemo } from 'react'
import { useParams, Link } from 'react-router'
import { useVirtualizer } from '@tanstack/react-virtual'
import { getAppLogs } from '../lib/api'

type LogLevel = 'ERROR' | 'WARN' | 'INFO' | 'DEBUG' | 'TRACE'

const LOG_LEVELS: LogLevel[] = ['ERROR', 'WARN', 'INFO', 'DEBUG', 'TRACE']

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
  [/\[(ERROR|WARN|INFO|DEBUG|TRACE)]/i, 1],
  [/\|-(ERROR|WARN|INFO|DEBUG|TRACE)\b/i, 1],
  [/\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\s+(ERROR|WARN|INFO|DEBUG|TRACE)\b/i, 1],
  [/\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[^\s]*\s+(ERROR|WARN|INFO|DEBUG|TRACE)\s/i, 1],
  [/^(ERROR|WARN|INFO|DEBUG|TRACE):/i, 1],
  [/\s(ERROR|WARN|INFO|DEBUG|TRACE)\s/i, 1],
  [/^(ERROR|WARN|INFO|DEBUG|TRACE)[\s:\-[]/i, 1],
]

function detectLevel(line: string): LogLevel | null {
  for (const [pattern, group] of LEVEL_PATTERNS) {
    const m = line.match(pattern)
    if (m) return m[group].toUpperCase() as LogLevel
  }
  return null
}

function detectLevelsForLines(lines: string[], lastLevel: LogLevel | null): { levels: (LogLevel | null)[], lastLevel: LogLevel | null } {
  const levels: (LogLevel | null)[] = []
  let current = lastLevel
  for (const line of lines) {
    const level = detectLevel(line)
    if (level) {
      current = level
      levels.push(level)
    } else {
      levels.push(current)
    }
  }
  return { levels, lastLevel: current }
}

function escapeRegExp(str: string): string {
  return str.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/** Test if a regex is safe (no catastrophic backtracking). Returns true if safe. */
function isRegexSafe(pattern: string): boolean {
  try {
    const re = new RegExp(pattern, 'i')
    // Non-matching suffix forces backtracking on catastrophic patterns like (a+)+$
    const testStr = 'a'.repeat(100) + '!'
    const start = performance.now()
    re.test(testStr)
    return performance.now() - start < 50
  } catch {
    return false
  }
}

const API_BASE = '/api/v1'
const MAX_LOG_LINES = 50_000

export function Logs() {
  const { name } = useParams<{ name: string }>()
  const [lines, setLines] = useState<string[]>([])
  const [levels, setLevels] = useState<(LogLevel | null)[]>([])
  const lastLevelRef = useRef<LogLevel | null>(null)
  const [tail, setTail] = useState(500)
  const [search, setSearch] = useState('')
  const [debouncedSearch, setDebouncedSearch] = useState('')
  const [useRegex, setUseRegex] = useState(false)
  const [follow, setFollow] = useState(true)
  const [wordWrap, setWordWrap] = useState(true)
  const [enabledLevels, setEnabledLevels] = useState<Set<LogLevel>>(new Set(LOG_LEVELS))
  const [error, setError] = useState<string | null>(null)
  const [matchIndex, setMatchIndex] = useState(0)
  const parentRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const abortRef = useRef<AbortController | null>(null)
  const followRef = useRef(follow)
  followRef.current = follow

  const setLinesWithLevels = useCallback((newLines: string[]) => {
    lastLevelRef.current = null
    const { levels: newLevels, lastLevel } = detectLevelsForLines(newLines, null)
    lastLevelRef.current = lastLevel
    setLines(newLines)
    setLevels(newLevels)
  }, [])

  const appendChunk = useCallback((chunk: string) => {
    setLines(prev => {
      const newLines = chunk.split('\n')
      // Merge last line fragment only if chunk continues a partial line
      let merged: string[]
      let newCount: number
      if (prev.length > 0 && newLines.length > 0 && !chunk.startsWith('\n')) {
        const lastLine = prev[prev.length - 1] + newLines[0]
        merged = [...prev.slice(0, -1), lastLine, ...newLines.slice(1)]
        newCount = newLines.length // 1 re-merged + rest
      } else {
        merged = [...prev, ...newLines]
        newCount = newLines.length
      }
      const trimmed = merged.length > MAX_LOG_LINES
      if (trimmed) {
        merged = merged.slice(-MAX_LOG_LINES)
      }
      if (trimmed) {
        // After trim, re-detect all levels (rare path — only on overflow)
        const { levels: allLevels, lastLevel } = detectLevelsForLines(merged, null)
        lastLevelRef.current = lastLevel
        setLevels(allLevels)
      } else {
        // Detect levels for new/changed lines only
        const appendStart = Math.max(0, merged.length - newCount)
        const appendedLines = merged.slice(appendStart)
        const { levels: newLevels, lastLevel } = detectLevelsForLines(appendedLines, lastLevelRef.current)
        lastLevelRef.current = lastLevel
        setLevels(prevLevels => {
          const kept = prevLevels.slice(0, appendStart)
          return [...kept, ...newLevels]
        })
      }
      return merged
    })
  }, [])

  // Debounce search input (300ms)
  useEffect(() => {
    const trimmed = search.slice(0, 200)
    const timer = setTimeout(() => setDebouncedSearch(trimmed), 300)
    return () => clearTimeout(timer)
  }, [search])

  // Initial load via REST, then stream via follow endpoint
  const fetchInitialLogs = useCallback(() => {
    if (!name) return
    getAppLogs(name, tail)
      .then((data) => {
        setLinesWithLevels(data.split('\n'))
        setError(null)
      })
      .catch((e) => setError(e.message))
  }, [name, tail, setLinesWithLevels])

  useEffect(() => {
    if (!follow) fetchInitialLogs()
  }, [fetchInitialLogs, follow])

  // Streaming follow using chunked response (not SSE, not polling)
  useEffect(() => {
    if (!follow || !name) return

    // Cancel previous stream
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller

    const startStream = async () => {
      try {
        const res = await fetch(`${API_BASE}/apps/${name}/logs?follow=true&tail=${tail}`, {
          signal: controller.signal,
        })
        if (!res.ok || !res.body) return

        const reader = res.body.getReader()
        const decoder = new TextDecoder()

        while (true) {
          const { done, value } = await reader.read()
          if (done) break
          const chunk = decoder.decode(value, { stream: true })
          appendChunk(chunk)
        }
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return
        // Reconnect after 3s on error
        setTimeout(() => {
          if (followRef.current) startStream()
        }, 3000)
      }
    }

    startStream()

    return () => {
      controller.abort()
    }
  }, [follow, name, tail, appendChunk])

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
        setLinesWithLevels([])
      }
      if (e.key === 'Escape') {
        setSearch('')
        searchRef.current?.blur()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [setLinesWithLevels])

  // Detect levels with inheritance, then filter
  const { filteredLines, filteredLevels, totalLineCount } = useMemo(() => {
    const entries = lines
      .map((line, i) => ({ line, level: levels[i] ?? null }))
      .filter(({ level }) => !level || enabledLevels.has(level))
    return {
      filteredLines: entries.map((e) => e.line),
      filteredLevels: entries.map((e) => e.level),
      totalLineCount: lines.length,
    }
  }, [lines, levels, enabledLevels])

  // Build safe search regex with ReDoS protection
  const { safeSearchRegex, regexWarning } = useMemo(() => {
    if (!debouncedSearch) {
      return { safeSearchRegex: null, regexWarning: null }
    }
    if (useRegex) {
      try {
        new RegExp(debouncedSearch, 'i') // syntax check
      } catch {
        return { safeSearchRegex: null, regexWarning: null }
      }
      if (!isRegexSafe(debouncedSearch)) {
        return {
          safeSearchRegex: new RegExp(escapeRegExp(debouncedSearch), 'gi'),
          regexWarning: 'Regex too complex — using literal match',
        }
      }
      return { safeSearchRegex: new RegExp(debouncedSearch, 'gi'), regexWarning: null }
    }
    return { safeSearchRegex: new RegExp(escapeRegExp(debouncedSearch), 'gi'), regexWarning: null }
  }, [debouncedSearch, useRegex])

  const searchMatches = useMemo(() => {
    const matches = new Set<number>()
    if (safeSearchRegex) {
      filteredLines.forEach((line, i) => {
        safeSearchRegex.lastIndex = 0
        if (safeSearchRegex.test(line)) matches.add(i)
      })
    }
    return matches
  }, [filteredLines, safeSearchRegex])

  const matchIndices = Array.from(searchMatches).sort((a, b) => a - b)
  const safeMatchIndex = matchIndices.length > 0
    ? ((matchIndex % matchIndices.length) + matchIndices.length) % matchIndices.length
    : 0

  useEffect(() => { setMatchIndex(0) }, [searchMatches])

  // Virtual list
  const virtualizer = useVirtualizer({
    count: filteredLines.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 20,
    overscan: 30,
  })

  // Scroll to bottom when following and new logs arrive
  useEffect(() => {
    if (follow && filteredLines.length > 0) {
      virtualizer.scrollToIndex(filteredLines.length - 1, { align: 'end' })
    }
  }, [filteredLines.length, follow, virtualizer])

  // Scroll to current search match
  useEffect(() => {
    if (matchIndices.length > 0) {
      const targetIdx = matchIndices[safeMatchIndex]
      if (targetIdx !== undefined) {
        virtualizer.scrollToIndex(targetIdx, { align: 'center' })
      }
    }
  }, [safeMatchIndex, matchIndices, virtualizer])

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
          {regexWarning && (
            <span className="text-xs text-warning">{regexWarning}</span>
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
          title="Stream logs (follow)"
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
          onClick={() => setLinesWithLevels([])}
          title="Clear logs (Ctrl+L)"
        >
          Clear
        </button>
        <button
          type="button"
          className="rounded px-2 py-1.5 text-xs border border-border text-muted-foreground hover:bg-muted"
          onClick={fetchInitialLogs}
        >
          Refresh
        </button>
      </div>

      {error && <div className="text-destructive text-sm mb-2">Error: {error}</div>}

      {/* Log output — virtualized */}
      <div
        ref={parentRef}
        className={`flex-1 overflow-auto rounded-lg border border-border bg-card p-4 font-mono text-xs ${wordWrap ? '' : 'overflow-x-auto'}`}
        onScroll={() => {
          if (!parentRef.current) return
          const { scrollTop, scrollHeight, clientHeight } = parentRef.current
          if (scrollHeight - scrollTop - clientHeight > 50) {
            setFollow(false)
          }
        }}
      >
        {filteredLines.length === 0 ? (
          <span className="text-muted-foreground">No logs available</span>
        ) : (
          <div
            style={{
              height: `${virtualizer.getTotalSize()}px`,
              width: '100%',
              position: 'relative',
            }}
          >
            {virtualizer.getVirtualItems().map((virtualItem) => {
              const idx = virtualItem.index
              const line = filteredLines[idx]
              const level = filteredLevels[idx]
              const colorClass = level ? LEVEL_COLORS[level] : 'text-foreground'
              const isCurrentMatch = matchIndices[safeMatchIndex] === idx

              return (
                <div
                  key={virtualItem.key}
                  data-index={idx}
                  ref={virtualizer.measureElement}
                  style={{
                    position: 'absolute',
                    top: 0,
                    left: 0,
                    width: '100%',
                    transform: `translateY(${virtualItem.start}px)`,
                  }}
                  className={`${colorClass} ${wordWrap ? 'whitespace-pre-wrap break-all' : 'whitespace-pre'} ${isCurrentMatch ? 'bg-primary/20' : searchMatches.has(idx) ? 'bg-primary/10' : ''}`}
                >
                  {safeSearchRegex ? highlightSearch(line, safeSearchRegex) : line}
                </div>
              )
            })}
          </div>
        )}
      </div>

      {/* Status bar */}
      <div className="mt-2 flex items-center justify-between text-xs text-muted-foreground">
        <span>
          {filteredLines.length} lines
          {filteredLines.length !== totalLineCount && ` (${totalLineCount} total)`}
        </span>
        <span>
          {follow && 'Streaming'} | Ctrl+F search | F3 next | Shift+F3 prev | Esc clear
        </span>
      </div>
    </div>
  )
}

function highlightSearch(line: string, regex: RegExp): React.ReactNode {
  regex.lastIndex = 0
  const parts = line.split(regex)
  if (parts.length === 1) return line
  regex.lastIndex = 0
  const matches = line.match(regex)
  if (!matches) return line
  const result: React.ReactNode[] = []
  for (let i = 0; i < parts.length; i++) {
    if (parts[i]) result.push(parts[i])
    if (i < matches.length) {
      result.push(
        <mark key={i} className="bg-warning/40 text-inherit rounded-sm px-0.5">
          {matches[i]}
        </mark>,
      )
    }
  }
  return result
}
