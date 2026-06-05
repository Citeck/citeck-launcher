import { useEffect, useState, useRef, useCallback, useMemo } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { ArrowDown } from 'lucide-react'
import { API_BASE } from '../lib/api'
import { useTranslation } from '../lib/i18n'

type LogLevel = 'ERROR' | 'WARN' | 'INFO' | 'DEBUG' | 'TRACE' | 'UNKNOWN'

const LOG_LEVELS: LogLevel[] = ['ERROR', 'WARN', 'INFO', 'DEBUG', 'TRACE', 'UNKNOWN']

// DEBUG is hidden by default — it's high-volume, low-signal for routine viewing
// (e.g. the daemon's per-request lines). The user can toggle it back on.
const DEFAULT_ENABLED_LEVELS: LogLevel[] = LOG_LEVELS.filter((l) => l !== 'DEBUG')

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
  // Kotlin parity (LogsViewer.kt — wordWrap defaulted to false): logs are
  // typically already line-oriented; soft-wrapping every long line by default
  // hides the indentation cues that make stack traces and JSON-payload lines
  // scannable. User can flip "Перенос" on when they want it.
  const [wordWrap, setWordWrap] = useState(false)
  const [enabledLevels, setEnabledLevels] = useState<Set<LogLevel>>(new Set(DEFAULT_ENABLED_LEVELS))
  const [matchIndex, setMatchIndex] = useState(0)
  // Tick incremented on EXPLICIT user navigation (typing a fresh query,
  // pressing Enter / F3 / Shift+F3, clicking the ↑/↓ buttons). The
  // scroll-to-match effect listens to this tick instead of safeMatchIndex so
  // incidental index resets (e.g. a new chunk shifting matchIndices) don't
  // snap the viewport and lock the user out of free scrolling.
  const [searchNavTick, setSearchNavTick] = useState(0)
  const bumpSearchNav = useCallback(() => setSearchNavTick((t) => t + 1), [])
  const parentRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const abortRef = useRef<AbortController | null>(null)
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Trailing partial line not yet terminated by '\n'. Held out of `lines` so
  // the rendered buffer only ever contains COMPLETE lines — no perpetual
  // trailing "" inflating the count (500, not 501) — while still joining lines
  // split across stream reads.
  const pendingRef = useRef('')
  // Current tail selection, mirrored into a ref so the []-dep appendChunk can
  // cap the live buffer without being recreated (which would restart the stream).
  const tailRef = useRef(tail)
  tailRef.current = tail
  const followRef = useRef(follow)
  followRef.current = follow
  const activeRef = useRef(active)
  activeRef.current = active
  // When we programmatically scroll (auto-stick-to-bottom, search recentre,
  // initial backlog landing), the browser still fires onScroll. Without this
  // gate the handler reads "scrollTop=0 + tall content" during the very
  // first paint and immediately disables follow — leaving the new window
  // pinned at the top instead of at the live tail. Pulse-high during the
  // animation frame following a programmatic scroll so the onScroll handler
  // can tell user input from layout-driven scrolling.
  const programmaticScrollRef = useRef(false)
  // Refs to copy/download functions so keyboard shortcuts can fire without
  // adding them to the keydown effect's deps (they capture filteredLines).
  const copyToClipboardRef = useRef<() => void>(undefined)
  const downloadRef = useRef<() => void>(undefined)

  const setLinesWithLevels = useCallback((newLines: string[]) => {
    // Drop the trailing empty element left when a backlog that ends in '\n' is
    // split, so the count matches the tail exactly (500, not 501). Resets the
    // partial-line buffer — the backlog is a complete, self-contained response.
    const trimmed = newLines.slice()
    while (trimmed.length > 0 && trimmed[trimmed.length - 1] === '') trimmed.pop()
    pendingRef.current = ''
    lastLevelRef.current = null
    const { levels: newLevels, lastLevel } = detectLevelsForLines(trimmed, null)
    lastLevelRef.current = lastLevel
    setLines(trimmed)
    setLevels(newLevels)
  }, [])

  const appendChunk = useCallback((chunk: string) => {
    // Buffer the trailing partial line in pendingRef; commit only complete
    // lines. This both joins lines split across reads and keeps `lines` free of
    // a perpetual trailing "".
    const combined = pendingRef.current + chunk
    const parts = combined.split('\n')
    pendingRef.current = parts.pop() ?? ''
    if (parts.length === 0) return
    const newLines = parts
    setLines(prev => {
      let merged = [...prev, ...newLines]
      // Cap the live buffer at the selected tail — oldest lines scroll off as
      // new ones arrive — bounded by the hard MAX_LOG_LINES safety ceiling.
      const cap = Math.min(MAX_LOG_LINES, Math.max(1, tailRef.current))
      const trimmed = merged.length > cap
      if (trimmed) {
        merged = merged.slice(-cap)
        const { levels: allLevels, lastLevel } = detectLevelsForLines(merged, null)
        lastLevelRef.current = lastLevel
        setLevels(allLevels)
      } else {
        const { levels: newLevels, lastLevel } = detectLevelsForLines(newLines, lastLevelRef.current)
        lastLevelRef.current = lastLevel
        setLevels(prevLevels => [...prevLevels, ...newLevels])
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

  // Streaming — the endpoint replays last `tail` lines then streams. First
  // chunk replaces state (backlog), subsequent chunks append. The stream is
  // kept open regardless of the `follow` UI toggle: follow now only controls
  // whether new chunks auto-scroll the viewport. Earlier we tore down the
  // stream on follow→false and re-fetched via REST instead, which (a) sent
  // the whole tail every time the user scrolled up, and (b) caused a visible
  // flicker between the stripped REST output and the raw stream output as
  // follow flipped (the REST path stripped ANSI, the stream did not).
  useEffect(() => {
    if (!appName || !active) return

    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller

    const base = source === 'daemon'
      ? `${API_BASE}/daemon/logs`
      : `${API_BASE}/apps/${appName}/logs`

    // Live tail: follow with tail=0 → only NEW lines, never a backlog burst.
    // The bulk backlog is fetched separately as a complete (closed) response.
    //
    // Why the split: the desktop webview reaches the daemon through the Wails
    // asset-server proxy (cmd/citeck-desktop/main.go). A long, flushed burst
    // (the whole `tail` backlog) leaves its tail stuck in the Wails/WebKitGTK
    // stream buffer — the buffer only drains to the fetch ReadableStream when
    // more bytes arrive or the response *closes*, so a follow stream that goes
    // idle right after the burst silently truncates the backlog (e.g. 144/200).
    // A non-follow GET closes, flushing the buffer in full; the follow stream
    // then carries only small live increments that each push the previous out.
    // Schedule a stream reconnect. Used on every non-aborted stream end —
    // crucially when the daemon closes the follow because the container was
    // recreated (a restarting app gets a NEW container id), or when the app has
    // no container yet (mid-restart). Without this the viewer froze at the
    // restart point ("logs stuck"): `done` just broke the loop and nothing
    // re-opened the stream on the new container.
    const reconnect = (delayMs: number) => {
      if (controller.signal.aborted) return
      retryTimerRef.current = setTimeout(() => {
        retryTimerRef.current = null
        if (activeRef.current) startLiveStream()
      }, delayMs)
    }
    const startLiveStream = async () => {
      try {
        const res = await fetch(`${base}?follow=true&tail=0`, { signal: controller.signal })
        if (!res.ok || !res.body) {
          reconnect(3000) // app likely has no container right now (mid-restart)
          return
        }
        const reader = res.body.getReader()
        const decoder = new TextDecoder()
        while (true) {
          const { done, value } = await reader.read()
          if (done) break
          appendChunk(decoder.decode(value, { stream: true }))
        }
        // Clean end → container was recreated/stopped: resume on the new one.
        reconnect(1000)
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return
        reconnect(3000)
      }
    }

    // Backlog: non-follow GET → a complete, closed response, delivered in full
    // over the asset bridge. Replaces state; the live stream then appends.
    const loadBacklogThenStream = async () => {
      try {
        const res = await fetch(`${base}?tail=${tail}`, { signal: controller.signal })
        if (res.ok) {
          const text = await res.text()
          setLinesWithLevels(text.split('\n'))
        }
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return
        // A transient backlog error shouldn't block live tailing — fall through.
      }
      if (controller.signal.aborted) return
      startLiveStream()
    }

    loadBacklogThenStream()

    return () => {
      controller.abort()
      if (retryTimerRef.current) {
        clearTimeout(retryTimerRef.current)
        retryTimerRef.current = null
      }
    }
  }, [appName, tail, appendChunk, setLinesWithLevels, active, source])

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
        bumpSearchNav()
        return
      }
      // Ctrl+L — clear (Kotlin parity)
      if (ctrl && !e.shiftKey && e.code === 'KeyL') {
        e.preventDefault()
        setLinesWithLevels([])
        return
      }
      // Ctrl+A — select only the log lines, not the whole window. The browser's
      // default select-all grabs the toolbar, status bar and window chrome too;
      // scope the selection to the log viewport instead. Skip when a text field
      // is focused so the search/filter inputs keep native select-all.
      if (ctrl && !e.shiftKey && e.code === 'KeyA') {
        const tag = (document.activeElement as HTMLElement | null)?.tagName
        if (tag === 'INPUT' || tag === 'TEXTAREA') return
        e.preventDefault()
        const el = parentRef.current
        if (el) {
          const sel = window.getSelection()
          sel?.removeAllRanges()
          const range = document.createRange()
          range.selectNodeContents(el)
          sel?.addRange(range)
          // Mark select-all so a subsequent copy grabs every filtered line, not
          // just the virtualized rows currently in the DOM.
          selectAllRef.current = true
        }
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
  }, [setLinesWithLevels, active, bumpSearchNav])

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

  // The log list is virtualized, so a native Ctrl+A selection only spans the
  // rendered rows. selectAllRef tracks an active "select all" so the copy
  // handler below can place the FULL filtered text on the clipboard; the ref
  // mirrors filteredLines for that synchronous copy-event read.
  const filteredLinesRef = useRef(filteredLines)
  filteredLinesRef.current = filteredLines
  const selectAllRef = useRef(false)

  // Override copy when select-all is active and the selection sits inside the
  // log viewport: hand over every filtered line, not just the rendered slice.
  useEffect(() => {
    const onCopy = (e: ClipboardEvent) => {
      if (!selectAllRef.current) return
      const sel = window.getSelection()
      if (!sel || sel.rangeCount === 0 || !parentRef.current?.contains(sel.anchorNode)) return
      e.preventDefault()
      e.clipboardData?.setData('text/plain', filteredLinesRef.current.join('\n'))
    }
    document.addEventListener('copy', onCopy)
    return () => document.removeEventListener('copy', onCopy)
  }, [])

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

  // Reset matchIndex when the query itself changes (not when matchIndices
  // just shifts due to new chunks).
  useEffect(() => {
    setMatchIndex(0)
    if (debouncedSearch) bumpSearchNav()
  }, [debouncedSearch, useRegex, bumpSearchNav])

  // eslint-disable-next-line react-hooks/incompatible-library -- useVirtualizer returns are consumed locally, no stale UI risk
  const virtualizer = useVirtualizer({
    count: filteredLines.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 20,
    overscan: 30,
  })

  // While Ctrl+A "select all" is active, re-apply the full-container selection
  // whenever the virtualized range changes (scroll / new lines) so every
  // freshly-mounted row shows as selected too — the range set once on Ctrl+A
  // only covers the rows that existed at that instant. (Copy already grabs
  // every filtered line via the copy handler; this is the matching visual.)
  const renderedItems = virtualizer.getVirtualItems()
  const renderedRangeKey = renderedItems.length
    ? `${renderedItems[0].index}:${renderedItems[renderedItems.length - 1].index}`
    : ''
  useEffect(() => {
    if (!selectAllRef.current || !parentRef.current) return
    const sel = window.getSelection()
    if (!sel) return
    sel.removeAllRanges()
    const range = document.createRange()
    range.selectNodeContents(parentRef.current)
    sel.addRange(range)
  }, [renderedRangeKey])

  // Refs so the layout effects don't have to list `virtualizer` (a fresh
  // instance every render) or `matchIndices` (a fresh array every chunk
  // arrival) in their dep arrays — without these, the scroll-to-index calls
  // re-fired on every render and yanked the viewport away from a user who
  // had scrolled up to read older lines.
  const virtualizerRef = useRef(virtualizer)
  virtualizerRef.current = virtualizer
  const matchIndicesRef = useRef(matchIndices)
  matchIndicesRef.current = matchIndices
  // safeMatchIndexRef mirrors safeMatchIndex so the scroll effect can read it
  // without making it a dependency (which would re-fire on every chunk arrival
  // that shifts matchIndices, hard-pinning the viewport and blocking free scroll).
  const safeMatchIndexRef = useRef(safeMatchIndex)
  safeMatchIndexRef.current = safeMatchIndex
  const prevFollowedLengthRef = useRef(filteredLines.length)

  function programmaticScrollTo(idx: number, align: 'start' | 'center' | 'end') {
    programmaticScrollRef.current = true
    virtualizerRef.current.scrollToIndex(idx, { align })
    // Clear after two animation frames so the onScroll event emitted by the
    // browser as a result of this call has time to run + be ignored.
    requestAnimationFrame(() => {
      requestAnimationFrame(() => { programmaticScrollRef.current = false })
    })
  }

  // Resume following: jump to the newest line and re-arm follow. Fired by the
  // floating button.
  function resumeFollow() {
    setFollow(true)
    const len = filteredLines.length
    if (len > 0) programmaticScrollTo(len - 1, 'end')
  }

  // Auto-scroll-to-bottom: only when follow is on AND the visible row count
  // actually grew. Plain re-renders (e.g. virtualizer re-measuring an item
  // in view) no longer trigger a scroll.
  useEffect(() => {
    const len = filteredLines.length
    if (follow && len > 0 && len !== prevFollowedLengthRef.current) {
      programmaticScrollTo(len - 1, 'end')
    }
    prevFollowedLengthRef.current = len
  }, [filteredLines.length, follow])

  // Keep the newest line pinned to the bottom while following when the viewport
  // resizes (window resize, panel drag, wrap toggle changing row heights).
  // A size change moves the scroll bottom without firing onScroll, so without
  // this the view would drift off the tail.
  useEffect(() => {
    const el = parentRef.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(() => {
      if (!followRef.current) return
      const len = filteredLinesRef.current.length
      if (len === 0) return
      programmaticScrollRef.current = true
      virtualizerRef.current.scrollToIndex(len - 1, { align: 'end' })
      requestAnimationFrame(() => {
        requestAnimationFrame(() => { programmaticScrollRef.current = false })
      })
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  // Search target: scroll only when the user explicitly navigated (the tick
  // bumps on a fresh query or F3 / Enter / ↑↓ button presses). Listening to
  // safeMatchIndex directly used to re-fire on every chunk arrival that
  // shifted matchIndices, hard-pinning the viewport on the current match
  // and making free scrolling impossible while a search was active.
  useEffect(() => {
    if (searchNavTick === 0) return
    const idxs = matchIndicesRef.current
    if (idxs.length === 0) return
    const targetIdx = idxs[safeMatchIndexRef.current]
    if (targetIdx === undefined) return
    programmaticScrollTo(targetIdx, 'center')
  }, [searchNavTick])

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
    <div className="flex flex-col h-full">
      {/* Toolbar */}
      <div className={`flex items-center gap-2 flex-wrap ${compact ? 'px-2 py-1' : 'mb-3'}`}>
        {/* Search */}
        <div className="flex items-center gap-1">
          <input
            ref={searchRef}
            type="text"
            value={search}
            onChange={(e) => { setSearch(e.target.value); setMatchIndex(0) }}
            onKeyDown={(e) => {
              // Enter → next match, Shift+Enter → previous. Matches the
              // standard browser find-bar behaviour and the Kotlin viewer's
              // F3 / Shift+F3 shortcut.
              if (e.key !== 'Enter') return
              e.preventDefault()
              setMatchIndex((p) => p + (e.shiftKey ? -1 : 1))
              bumpSearchNav()
            }}
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
          {/* Match nav is rendered unconditionally and disabled when there are
              no matches — otherwise the controls pop in/out and the whole
              toolbar jumps sideways whenever the match count crosses zero
              (e.g. toggling a level filter that hides every current match). */}
          <button type="button" disabled={matchIndices.length === 0}
            className={`rounded px-1.5 py-1 text-xs border border-border ${matchIndices.length === 0 ? 'text-muted-foreground/40 cursor-not-allowed' : 'text-muted-foreground hover:bg-muted'}`}
            onClick={() => { setMatchIndex((p) => p - 1); bumpSearchNav() }} title={t('logViewer.prevMatch')}>&uarr;</button>
          <button type="button" disabled={matchIndices.length === 0}
            className={`rounded px-1.5 py-1 text-xs border border-border ${matchIndices.length === 0 ? 'text-muted-foreground/40 cursor-not-allowed' : 'text-muted-foreground hover:bg-muted'}`}
            onClick={() => { setMatchIndex((p) => p + 1); bumpSearchNav() }} title={t('logViewer.nextMatch')}>&darr;</button>
          <span className="text-xs text-muted-foreground tabular-nums">{matchIndices.length === 0 ? 0 : safeMatchIndex + 1}/{matchIndices.length}</span>
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

        {/* Tail lines — appearance:none kills the OS-native chevron / popup
            so the dropdown stays themed even in secondary Wails windows
            (otherwise WebKit-GTK falls back to system colors and renders a
            light, oversized control). The custom chevron is layered via
            background-image so the trigger stays just text + arrow. */}
        <select
          value={tail}
          onChange={(e) => setTail(Number(e.target.value))}
          className="appearance-none rounded border border-border bg-card pl-2 pr-6 py-1 text-xs text-foreground focus:outline-none focus:border-primary cursor-pointer"
          style={{
            backgroundImage:
              "url(\"data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='10' height='6' viewBox='0 0 10 6'%3E%3Cpath fill='%239da0a8' d='M0 0l5 6 5-6z'/%3E%3C/svg%3E\")",
            backgroundPosition: 'right 6px center',
            backgroundRepeat: 'no-repeat',
            backgroundSize: '8px 5px',
          }}
        >
          <option value={100} className="bg-card text-foreground">100</option>
          <option value={200} className="bg-card text-foreground">200</option>
          <option value={500} className="bg-card text-foreground">500</option>
          <option value={1000} className="bg-card text-foreground">1000</option>
          <option value={5000} className="bg-card text-foreground">5000</option>
        </select>

        <div className="h-5 w-px bg-border" />

        {/* Toggles — "Follow" is now a floating button at the bottom-right of
            the log viewport (shown only when follow is off), so it isn't here. */}
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
      </div>

      {/* Log output — virtualized. Wrapped in a relative box so the floating
          "follow" button can sit at the bottom-right of the viewport. */}
      <div className={`relative flex-1 min-h-0 ${compact ? 'mx-2 mb-1' : ''}`}>
      <div
        ref={parentRef}
        className={`h-full w-full overflow-auto rounded-lg border border-border bg-card p-4 font-mono text-xs ${wordWrap ? '' : 'overflow-x-auto'}`}
        onScroll={() => {
          if (!parentRef.current) return
          // Programmatic scroll (auto-stick / search recentre) also fires
          // onScroll. Ignore those — otherwise the initial backlog landing
          // would flip follow off the moment the viewport sat at scrollTop=0
          // with a tall total size, leaving the new window pinned at the
          // start instead of at the live tail.
          if (programmaticScrollRef.current) return
          const { scrollTop, scrollHeight, clientHeight } = parentRef.current
          // Stick-to-bottom: drop follow when the user scrolls up past
          // ~50px, re-arm it when they scroll back to the bottom.
          const distFromBottom = scrollHeight - scrollTop - clientHeight
          if (distFromBottom > 50) {
            if (followRef.current) setFollow(false)
          } else {
            if (!followRef.current) setFollow(true)
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
            // mousedown on the actual content (not the scrollbar) starts a
            // manual selection → cancel the Ctrl+A "select all" so copy returns
            // just the user's range. Scrollbar drags land on the parent, not
            // here, so scrolling keeps select-all alive.
            onMouseDown={() => { selectAllRef.current = false }}
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
        {/* Floating follow button — only while NOT following. Click resumes
            follow AND jumps to the newest line. */}
        {!follow && (
          <button
            type="button"
            onClick={resumeFollow}
            title={t('logViewer.follow.tooltip')}
            className="absolute bottom-3 right-4 z-10 flex items-center gap-1 rounded-full border border-border bg-card/95 px-3 py-1.5 text-xs text-foreground shadow-lg backdrop-blur hover:bg-muted"
          >
            <ArrowDown size={14} />
            {t('logViewer.follow')}
          </button>
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
