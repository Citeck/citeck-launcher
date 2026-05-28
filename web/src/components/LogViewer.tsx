import { useEffect, useState, useRef, useCallback, useMemo } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { getAppLogs, getDaemonLogs, API_BASE } from '../lib/api'
import { useTranslation } from '../lib/i18n'

type LogLevel = 'ERROR' | 'WARN' | 'INFO' | 'DEBUG' | 'TRACE' | 'UNKNOWN'

const LOG_LEVELS: LogLevel[] = ['ERROR', 'WARN', 'INFO', 'DEBUG', 'TRACE', 'UNKNOWN']

const LEVEL_COLORS: Record<LogLevel, string> = {
  ERROR: 'text-[#ef5350]',
  WARN: 'text-[#ffa726]',
  INFO: 'text-[#66bb6a]',
  DEBUG: 'text-[#9e9e9e]',
  TRACE: 'text-[#bdbdbd]',
  UNKNOWN: 'text-[#7c7c7c]',
}

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

const NESTED_QUANTIFIER_RE = /([+*]|\{\d)[^)]*\)\s*[+*{]/

function isRegexSafe(pattern: string): boolean {
  try {
    new RegExp(pattern, 'i')
    return !NESTED_QUANTIFIER_RE.test(pattern)
  } catch {
    return false
  }
}

const MAX_LOG_LINES = 50_000

interface LogViewerProps {
  appName: string
  compact?: boolean
  active?: boolean
  /** 'app' (default) = /apps/{name}/logs; 'daemon' = /daemon/logs. */
  source?: 'app' | 'daemon'
}

export function LogViewer({ appName, compact = false, active = true, source = 'app' }: LogViewerProps) {
  const { t } = useTranslation()
  const [lines, setLines] = useState<string[]>([])
  const [levels, setLevels] = useState<(LogLevel | null)[]>([])
  const lastLevelRef = useRef<LogLevel | null>(null)
  const [tail, setTail] = useState(500)
  const [search, setSearch] = useState('')
  const [debouncedSearch, setDebouncedSearch] = useState('')
  const [filterText, setFilterText] = useState('')
  const [debouncedFilter, setDebouncedFilter] = useState('')
  const [useRegex, setUseRegex] = useState(false)
  const [follow, setFollow] = useState(true)
  const [wordWrap, setWordWrap] = useState(true)
  const [enabledLevels, setEnabledLevels] = useState<Set<LogLevel>>(new Set(LOG_LEVELS))
  const [error, setError] = useState<string | null>(null)
  const [matchIndex, setMatchIndex] = useState(0)
  const parentRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const abortRef = useRef<AbortController | null>(null)
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const followRef = useRef(follow)
  followRef.current = follow
  const activeRef = useRef(active)
  activeRef.current = active
  // Refs to copy/download functions so keyboard shortcuts can fire without
  // adding them to the keydown effect's deps (they capture filteredLines).
  const copyToClipboardRef = useRef<() => void>(undefined)
  const downloadRef = useRef<() => void>(undefined)

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
      let merged: string[]
      let newCount: number
      if (prev.length > 0 && newLines.length > 0 && !chunk.startsWith('\n')) {
        const lastLine = prev[prev.length - 1] + newLines[0]
        merged = [...prev.slice(0, -1), lastLine, ...newLines.slice(1)]
        newCount = newLines.length
      } else {
        merged = [...prev, ...newLines]
        newCount = newLines.length
      }
      const trimmed = merged.length > MAX_LOG_LINES
      if (trimmed) {
        merged = merged.slice(-MAX_LOG_LINES)
      }
      if (trimmed) {
        const { levels: allLevels, lastLevel } = detectLevelsForLines(merged, null)
        lastLevelRef.current = lastLevel
        setLevels(allLevels)
      } else {
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

  // Abort streaming when deactivated
  useEffect(() => {
    if (!active) {
      abortRef.current?.abort()
    }
  }, [active])

  // Debounce search input (300ms)
  useEffect(() => {
    const trimmed = search.slice(0, 200)
    const timer = setTimeout(() => setDebouncedSearch(trimmed), 300)
    return () => clearTimeout(timer)
  }, [search])

  useEffect(() => {
    const trimmed = filterText.slice(0, 200)
    const timer = setTimeout(() => setDebouncedFilter(trimmed), 300)
    return () => clearTimeout(timer)
  }, [filterText])

  // Initial load via REST, then stream via follow endpoint
  const fetchInitialLogs = useCallback(() => {
    if (!appName) return
    const fetcher = source === 'daemon' ? getDaemonLogs(tail) : getAppLogs(appName, tail)
    fetcher
      .then((data) => {
        setLinesWithLevels(data.split('\n'))
        setError(null)
      })
      .catch((e) => setError(e.message))
  }, [appName, tail, setLinesWithLevels, source])

  // Non-follow mode: load via REST
  useEffect(() => {
    if (!follow && active) fetchInitialLogs()
  }, [fetchInitialLogs, follow, active])

  // Streaming follow — the endpoint replays last `tail` lines then streams.
  // First chunk replaces state (backlog), subsequent chunks append.
  useEffect(() => {
    if (!follow || !appName || !active) return

    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller

    const streamUrl = source === 'daemon'
      ? `${API_BASE}/daemon/logs?follow=true&tail=${tail}`
      : `${API_BASE}/apps/${appName}/logs?follow=true&tail=${tail}`

    const startStream = async () => {
      try {
        const res = await fetch(streamUrl, {
          signal: controller.signal,
        })
        if (!res.ok || !res.body) return

        const reader = res.body.getReader()
        const decoder = new TextDecoder()
        let isFirst = true

        while (true) {
          const { done, value } = await reader.read()
          if (done) break
          const chunk = decoder.decode(value, { stream: true })
          if (isFirst) {
            // First chunk is the tail backlog — replace state to avoid duplication
            setLinesWithLevels(chunk.split('\n'))
            isFirst = false
          } else {
            appendChunk(chunk)
          }
        }
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return
        retryTimerRef.current = setTimeout(() => {
          retryTimerRef.current = null
          if (followRef.current && activeRef.current) startStream()
        }, 3000)
      }
    }

    startStream()

    return () => {
      controller.abort()
      if (retryTimerRef.current) {
        clearTimeout(retryTimerRef.current)
        retryTimerRef.current = null
      }
    }
  }, [follow, appName, tail, appendChunk, setLinesWithLevels, active, source])

  // Keyboard shortcuts — match Kotlin LogsWindow.
  useEffect(() => {
    if (!active) return
    function handleKeyDown(e: KeyboardEvent) {
      const ctrl = e.ctrlKey || e.metaKey
      // Ctrl+F: focus search and SELECT all (Kotlin parity — next keypress replaces query).
      // e.code is layout-invariant so Cyrillic/other non-Latin layouts still match.
      if (ctrl && !e.shiftKey && e.code === 'KeyF') {
        e.preventDefault()
        const el = searchRef.current
        if (el) {
          el.focus()
          el.select()
        }
        return
      }
      // F3 / Ctrl+G — next match; Shift+F3 / Ctrl+Shift+G — prev
      if (e.key === 'F3' || (ctrl && e.code === 'KeyG')) {
        e.preventDefault()
        setMatchIndex((prev) => prev + (e.shiftKey ? -1 : 1))
        return
      }
      // Ctrl+L — clear (Kotlin parity)
      if (ctrl && !e.shiftKey && e.code === 'KeyL') {
        e.preventDefault()
        setLinesWithLevels([])
        return
      }
      // Ctrl+Shift+C — copy all visible (Kotlin LogsToolbar shortcut)
      if (ctrl && e.shiftKey && e.code === 'KeyC') {
        e.preventDefault()
        copyToClipboardRef.current?.()
        return
      }
      // Ctrl+S — export to file (Kotlin LogsToolbar shortcut)
      if (ctrl && !e.shiftKey && e.code === 'KeyS') {
        e.preventDefault()
        downloadRef.current?.()
        return
      }
      if (e.key === 'Escape') {
        setSearch('')
        searchRef.current?.blur()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [setLinesWithLevels, active])

  // Kotlin parity: lines without a parsed level fall into the UNKNOWN bucket
  // and respect its dedicated toggle (LogsViewer.kt:151-158).
  const { filteredLines, filteredLevels, totalLineCount } = useMemo(() => {
    let pattern: RegExp | null = null
    if (debouncedFilter && debouncedFilter.length >= 2) {
      try {
        const escaped = debouncedFilter.replace(/[.+?^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*')
        pattern = new RegExp(escaped, 'i')
      } catch {
        pattern = null
      }
    }
    const entries = lines
      .map((line, i) => ({ line, level: (levels[i] ?? null) as LogLevel | null }))
      .filter(({ line, level }) => {
        const bucket: LogLevel = level ?? 'UNKNOWN'
        if (!enabledLevels.has(bucket)) return false
        if (pattern && !pattern.test(line)) return false
        return true
      })
    return {
      filteredLines: entries.map((e) => e.line),
      filteredLevels: entries.map((e) => e.level),
      totalLineCount: lines.length,
    }
  }, [lines, levels, enabledLevels, debouncedFilter])

  const { safeSearchRegex, regexWarning } = useMemo(() => {
    if (!debouncedSearch) {
      return { safeSearchRegex: null, regexWarning: null }
    }
    if (useRegex) {
      try {
        new RegExp(debouncedSearch, 'i')
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

  // eslint-disable-next-line react-hooks/incompatible-library -- useVirtualizer returns are consumed locally, no stale UI risk
  const virtualizer = useVirtualizer({
    count: filteredLines.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 20,
    overscan: 30,
  })

  useEffect(() => {
    if (follow && filteredLines.length > 0) {
      virtualizer.scrollToIndex(filteredLines.length - 1, { align: 'end' })
    }
  }, [filteredLines.length, follow, virtualizer])

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
  copyToClipboardRef.current = copyToClipboard

  function downloadLogs() {
    const blob = new Blob([filteredLines.join('\n')], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    // Kotlin's default filename pattern: "<windowTitle>_<yyyyMMdd_HHmmss>.log"
    const d = new Date()
    const pad = (n: number) => String(n).padStart(2, '0')
    const ts = `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}_${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`
    a.download = source === 'daemon' ? `daemon_${ts}.log` : `${appName}_${ts}.log`
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 5000)
  }
  downloadRef.current = downloadLogs

  return (
    <div className={compact ? 'flex flex-col h-full' : 'flex flex-col h-[calc(100vh-100px)]'}>
      {/* Toolbar */}
      <div className={`flex items-center gap-2 flex-wrap ${compact ? 'px-2 py-1' : 'mb-3'}`}>
        {/* Search */}
        <div className="flex items-center gap-1">
          <input
            ref={searchRef}
            type="text"
            value={search}
            onChange={(e) => { setSearch(e.target.value); setMatchIndex(0) }}
            placeholder={t('logViewer.search')}
            className={`rounded-md border border-border bg-card px-2 py-1 text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-primary ${compact ? 'w-40 text-xs' : 'w-56 text-sm py-1.5 px-3'}`}
          />
          <button
            type="button"
            className={`rounded px-2 py-1 text-xs border ${useRegex ? 'border-primary bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:bg-muted'}`}
            onClick={() => setUseRegex(!useRegex)}
            title={t('logViewer.toggleRegex')}
          >
            .*
          </button>
          {matchIndices.length > 0 && (
            <>
              <button type="button" className="rounded px-1.5 py-1 text-xs border border-border text-muted-foreground hover:bg-muted"
                onClick={() => setMatchIndex((p) => p - 1)} title={t('logViewer.prevMatch')}>&uarr;</button>
              <button type="button" className="rounded px-1.5 py-1 text-xs border border-border text-muted-foreground hover:bg-muted"
                onClick={() => setMatchIndex((p) => p + 1)} title={t('logViewer.nextMatch')}>&darr;</button>
              <span className="text-xs text-muted-foreground">{safeMatchIndex + 1}/{matchIndices.length}</span>
            </>
          )}
          {regexWarning && <span className="text-xs text-warning">{t('logViewer.regexWarning')}</span>}
        </div>

        <div className="h-5 w-px bg-border" />

        {/* Filter (wildcard, hides non-matching lines — Kotlin LogsViewer.kt:217) */}
        <input
          type="text"
          value={filterText}
          onChange={(e) => setFilterText(e.target.value)}
          placeholder={t('logViewer.filter')}
          title={t('logViewer.filter.tooltip')}
          className={`rounded-md border border-border bg-card px-2 py-1 text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-primary ${compact ? 'w-32 text-xs' : 'w-44 text-sm py-1.5 px-3'}`}
        />

        <div className="h-5 w-px bg-border" />

        {/* Level filters */}
        <div className="flex items-center gap-1">
          {LOG_LEVELS.map((level) => (
            <button
              key={level}
              type="button"
              className={`rounded px-2 py-1 text-xs font-medium ${
                enabledLevels.has(level) ? `${LEVEL_COLORS[level]} bg-muted` : 'text-muted-foreground/50 line-through'
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
          className="rounded-md border border-border bg-card px-2 py-1 text-xs text-foreground"
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
          className={`rounded px-2 py-1 text-xs border ${follow ? 'border-primary bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:bg-muted'}`}
          onClick={() => setFollow(!follow)}
          title={t('logViewer.follow.tooltip')}
        >
          {t('logViewer.follow')}
        </button>
        <button
          type="button"
          className={`rounded px-2 py-1 text-xs border ${wordWrap ? 'border-primary bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:bg-muted'}`}
          onClick={() => setWordWrap(!wordWrap)}
          title={t('logViewer.wrap.tooltip')}
        >
          {t('logViewer.wrap')}
        </button>

        <div className="flex-1" />

        {/* Actions */}
        <button type="button" className="rounded px-2 py-1 text-xs border border-border text-muted-foreground hover:bg-muted"
          onClick={copyToClipboard} title={t('logViewer.copy.tooltip')}>{t('logViewer.copy')}</button>
        <button type="button" className="rounded px-2 py-1 text-xs border border-border text-muted-foreground hover:bg-muted"
          onClick={downloadLogs} title={t('logViewer.download.tooltip')}>{t('logViewer.download')}</button>
        <button type="button" className="rounded px-2 py-1 text-xs border border-border text-muted-foreground hover:bg-muted"
          onClick={() => setLinesWithLevels([])} title={t('logViewer.clear.tooltip')}>{t('logViewer.clear')}</button>
        <button type="button" className="rounded px-2 py-1 text-xs border border-border text-muted-foreground hover:bg-muted"
          onClick={fetchInitialLogs}>{t('logViewer.refresh')}</button>
      </div>

      {error && <div className="text-destructive text-sm mb-2 px-2">{t('common.error', { error })}</div>}

      {/* Log output — virtualized */}
      <div
        ref={parentRef}
        className={`flex-1 overflow-auto rounded-lg border border-border bg-card p-4 font-mono text-xs ${wordWrap ? '' : 'overflow-x-auto'} ${compact ? 'mx-2 mb-1' : ''}`}
        onScroll={() => {
          if (!parentRef.current) return
          const { scrollTop, scrollHeight, clientHeight } = parentRef.current
          if (scrollHeight - scrollTop - clientHeight > 50) {
            setFollow(false)
          }
        }}
      >
        {filteredLines.length === 0 ? (
          <span className="text-muted-foreground">{t('logViewer.empty')}</span>
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
                  className={`${colorClass} ${wordWrap ? 'whitespace-pre-wrap break-all' : 'whitespace-pre'} ${isCurrentMatch ? 'bg-primary/10' : ''}`}
                >
                  {safeSearchRegex ? highlightSearch(line, safeSearchRegex, isCurrentMatch) : line}
                </div>
              )
            })}
          </div>
        )}
      </div>

      {/* Status bar */}
      <div className={`flex items-center justify-between text-xs text-muted-foreground ${compact ? 'px-2 py-0.5' : 'mt-2'}`}>
        <span>
          {filteredLines.length !== totalLineCount
            ? t('logViewer.linesTotal', { count: filteredLines.length, total: totalLineCount })
            : t('logViewer.lines', { count: filteredLines.length })}
        </span>
        <span>
          {follow && t('logViewer.streaming')} | {t('logViewer.shortcuts')}
        </span>
      </div>
    </div>
  )
}

function highlightSearch(line: string, regex: RegExp, isCurrent: boolean): React.ReactNode {
  regex.lastIndex = 0
  const parts = line.split(regex)
  if (parts.length === 1) return line
  regex.lastIndex = 0
  const matches = line.match(regex)
  if (!matches) return line
  // Kotlin parity (LogsComponents.kt LogLevelColors): current match → #FF9800
  // orange on black; other matches → #FFEB3B yellow on black. Both colors are
  // applied via inline `style` (not Tailwind classes) so the pair is visible
  // side by side without hopping between tailwind.config and palette.
  const markStyle = {
    backgroundColor: isCurrent ? '#FF9800' : '#FFEB3B',
    color: '#000',
  }
  const markClass = 'rounded-sm px-0.5'
  const result: React.ReactNode[] = []
  for (let i = 0; i < parts.length; i++) {
    if (parts[i]) result.push(parts[i])
    if (i < matches.length) {
      result.push(
        <mark key={i} className={markClass} style={markStyle}>
          {matches[i]}
        </mark>,
      )
    }
  }
  return result
}
