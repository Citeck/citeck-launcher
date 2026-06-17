import { useCallback, useEffect, useRef, useState } from 'react'
import { API_BASE } from '../lib/api'

export type LogLevel = 'ERROR' | 'WARN' | 'INFO' | 'DEBUG' | 'TRACE' | 'UNKNOWN'

export const LOG_LEVELS: LogLevel[] = ['ERROR', 'WARN', 'INFO', 'DEBUG', 'TRACE', 'UNKNOWN']

// Shared by the toolbar level toggles (LogViewer) and the line rendering
// (LogViewport). Lives here — not in a component file — so react-refresh's
// only-export-components rule stays happy.
// Theme-aware via CSS vars (defined in index.css): the dark defaults match the
// original hex; the [data-theme="light"] overrides darken INFO/DEBUG/TRACE so
// they don't wash out on the near-white log viewport.
export const LEVEL_COLORS: Record<LogLevel, string> = {
  ERROR: 'text-[var(--color-log-error)]',
  WARN: 'text-[var(--color-log-warn)]',
  INFO: 'text-[var(--color-log-info)]',
  DEBUG: 'text-[var(--color-log-debug)]',
  TRACE: 'text-[var(--color-log-trace)]',
  UNKNOWN: 'text-[var(--color-log-unknown)]',
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

export function detectLevel(line: string): LogLevel | null {
  for (const [pattern, group] of LEVEL_PATTERNS) {
    const m = line.match(pattern)
    if (m) return m[group].toUpperCase() as LogLevel
  }
  return null
}

/**
 * Detects the level for each line; lines without a recognizable level marker
 * (stack-trace frames, JSON payload continuations) inherit the level of the
 * preceding line, seeded with `carry`.
 */
export function detectLevelsForLines(lines: string[], carry: LogLevel | null): (LogLevel | null)[] {
  const levels: (LogLevel | null)[] = []
  let current = carry
  for (const line of lines) {
    const level = detectLevel(line)
    if (level) current = level
    levels.push(level ?? current)
  }
  return levels
}

export const MAX_LOG_LINES = 50_000

/**
 * Lines and their detected levels live in ONE immutable state object so they
 * can never desync: every update derives both arrays together inside a pure
 * updater (StrictMode double-invoke safe). The level carried into a new chunk
 * is simply the level of the last buffered line — continuation lines inherit
 * their predecessor's level, so no extra "last level" bookkeeping is needed.
 */
export interface LogBuffer {
  lines: string[]
  levels: (LogLevel | null)[]
}

const EMPTY_BUFFER: LogBuffer = { lines: [], levels: [] }

/**
 * Pure half of appendChunk's partial-line handling: joins the pending partial
 * line with the new chunk and splits off COMPLETE lines, returning the new
 * trailing partial (the text after the last '\n', possibly ''). Keeping it
 * pure makes the split-across-reads behaviour table-testable.
 */
export function splitChunkLines(pending: string, chunk: string): { complete: string[]; pending: string } {
  const parts = (pending + chunk).split('\n')
  const nextPending = parts.pop() ?? ''
  return { complete: parts, pending: nextPending }
}

/**
 * Pure core of appendChunk's buffer update: appends complete lines, derives
 * their levels from the previous buffer's trailing level (continuation lines
 * inherit), and caps the buffer at the selected tail bounded by the
 * MAX_LOG_LINES safety ceiling (oldest lines scroll off).
 */
export function appendLinesToBuffer(prev: LogBuffer, parts: string[], tail: number): LogBuffer {
  const carry = prev.levels.length > 0 ? prev.levels[prev.levels.length - 1] : null
  let lines = [...prev.lines, ...parts]
  let levels = [...prev.levels, ...detectLevelsForLines(parts, carry)]
  const cap = Math.min(MAX_LOG_LINES, Math.max(1, tail))
  if (lines.length > cap) {
    lines = lines.slice(-cap)
    levels = levels.slice(-cap)
  }
  return { lines, levels }
}

export interface UseLogStreamOptions {
  appName: string
  /** 'app' (default) = /apps/{name}/logs; 'daemon' = /daemon/logs. */
  source?: 'app' | 'daemon'
  /** When false the stream is aborted and not re-opened. */
  active?: boolean
  /** Backlog size; also caps the live buffer (oldest lines scroll off). */
  tail: number
}

/**
 * useLogStream owns the log line buffer and the backlog + live-tail streaming
 * lifecycle for one log source.
 *
 * Streaming model: the endpooint replays the last `tail` lines as a complete
 * (closed) non-follow GET, then a `follow=true&tail=0` stream appends only NEW
 * lines. The split matters for the desktop webview: the Wails/WebKitGTK asset
 * bridge only drains its stream buffer when more bytes arrive or the response
 * closes, so a follow stream that bursts the whole backlog and goes idle
 * silently truncates it. A closed backlog response flushes in full; the follow
 * stream then carries only small live increments.
 *
 * The stream reconnects on every non-aborted end — crucially when the daemon
 * closes the follow because the container was recreated (a restarting app gets
 * a NEW container id), or when the app has no container yet (mid-restart).
 */
export function useLogStream({ appName, source = 'app', active = true, tail }: UseLogStreamOptions) {
  const [buffer, setBuffer] = useState<LogBuffer>(EMPTY_BUFFER)
  const abortRef = useRef<AbortController | null>(null)
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Trailing partial line not yet terminated by '\n'. Held out of the buffer
  // so the rendered lines only ever contain COMPLETE lines — no perpetual
  // trailing "" inflating the count (500, not 501) — while still joining
  // lines split across stream reads.
  const pendingRef = useRef('')
  // Current tail selection, mirrored into a ref so the []-dep appendChunk can
  // cap the live buffer without being recreated (which would restart the stream).
  const tailRef = useRef(tail)
  const activeRef = useRef(active)
  useEffect(() => {
    tailRef.current = tail
    activeRef.current = active
  }, [tail, active])

  /** Replaces the buffer with a complete backlog response (or clears it). */
  const setAllLines = useCallback((newLines: string[]) => {
    // Drop the trailing empty element left when a backlog that ends in '\n' is
    // split, so the count matches the tail exactly. Resets the partial-line
    // buffer — the backlog is a complete, self-contained response.
    const trimmed = newLines.slice()
    while (trimmed.length > 0 && trimmed[trimmed.length - 1] === '') trimmed.pop()
    pendingRef.current = ''
    setBuffer({ lines: trimmed, levels: detectLevelsForLines(trimmed, null) })
  }, [])

  const clear = useCallback(() => setAllLines([]), [setAllLines])

  const appendChunk = useCallback((chunk: string) => {
    // Buffer the trailing partial line in pendingRef; commit only complete
    // lines. This both joins lines split across reads and keeps the buffer
    // free of a perpetual trailing "". The actual splitting/capping logic is
    // pure (splitChunkLines / appendLinesToBuffer) so it stays unit-testable.
    const { complete, pending } = splitChunkLines(pendingRef.current, chunk)
    pendingRef.current = pending
    if (complete.length === 0) return
    setBuffer((prev) => appendLinesToBuffer(prev, complete, tailRef.current))
  }, [])

  // Abort streaming when deactivated
  useEffect(() => {
    if (!active) {
      abortRef.current?.abort()
    }
  }, [active])

  // Streaming — backlog first, then live tail. The stream is kept open
  // regardless of the caller's follow toggle: follow only controls whether
  // new chunks auto-scroll the viewport (see LogViewport).
  useEffect(() => {
    if (!appName || !active) return

    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller

    const base = source === 'daemon'
      ? `${API_BASE}/daemon/logs`
      : `${API_BASE}/apps/${encodeURIComponent(appName)}/logs`

    // Schedule a stream reconnect. Used on every non-aborted stream end —
    // without this the viewer froze at a container-recreate point ("logs
    // stuck"): `done` just broke the loop and nothing re-opened the stream
    // on the new container.
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
          setAllLines(text.split('\n'))
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
  }, [appName, tail, appendChunk, setAllLines, active, source])

  return { lines: buffer.lines, levels: buffer.levels, clear }
}
