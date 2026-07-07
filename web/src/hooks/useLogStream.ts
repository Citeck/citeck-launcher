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

/** Coalescing window: live chunks are buffered and applied at most this often. */
export const LOG_FLUSH_INTERVAL_MS = 80

/**
 * One buffered log line. `id` is a monotonic sequence number assigned when the
 * line enters the buffer and NEVER reused — the virtualizer keys rows by it,
 * so row identity (React nodes, measured heights, text selection) follows the
 * LINE across front-trims instead of the buffer slot index.
 */
export interface LogEntry {
  id: number
  text: string
  level: LogLevel | null
}

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

/** Builds entries for `lines` with sequential ids from `startId`, carrying levels. */
export function makeEntries(lines: string[], carry: LogLevel | null, startId: number): LogEntry[] {
  const levels = detectLevelsForLines(lines, carry)
  return lines.map((text, i) => ({ id: startId + i, text, level: levels[i] }))
}

/**
 * Pure core of the flush: appends complete lines as entries (continuation
 * lines inherit the buffer's trailing level) and caps the buffer, dropping the
 * OLDEST entries. Surviving entries keep their ids — that invariant is what
 * the whole scroll/selection stability rests on.
 */
export function appendEntriesToBuffer(prev: LogEntry[], lines: string[], startId: number, cap: number): LogEntry[] {
  const carry = prev.length > 0 ? prev[prev.length - 1].level : null
  const next = [...prev, ...makeEntries(lines, carry, startId)]
  const c = Math.max(1, cap)
  return next.length > c ? next.slice(-c) : next
}

/**
 * Largest k such that the last k lines of `prev` equal the first k lines of
 * `next`. Used to merge the backlog with live lines HELD while the backlog was
 * loading: the live stream attaches before the backlog is read, so a line
 * emitted in between appears in both — the overlap is dropped from the held
 * side. Preferring the LARGEST k means a pathological all-identical-lines
 * stream may drop a legit duplicate, which beats replaying the whole run.
 */
export function overlapLineCount(prev: string[], next: string[]): number {
  for (let k = Math.min(prev.length, next.length); k > 0; k--) {
    let match = true
    for (let i = 0; i < k; i++) {
      if (prev[prev.length - k + i] !== next[i]) {
        match = false
        break
      }
    }
    if (match) return k
  }
  return 0
}

/**
 * Buffer cap for the current follow state. While following, the buffer is a
 * sliding window of `tail` lines. While NOT following the user is reading —
 * front-trimming would slide the content under their viewport (the "logs jump
 * while I'm in the middle" bug), so the window FREEZES and only the
 * MAX_LOG_LINES safety ceiling applies; the tail cap is re-applied when
 * follow resumes (the viewport jumps to the bottom then anyway).
 */
export function effectiveCap(tail: number, follow: boolean): number {
  return follow ? Math.min(MAX_LOG_LINES, Math.max(1, tail)) : MAX_LOG_LINES
}

export interface UseLogStreamOptions {
  appName: string
  /** 'app' (default) = /apps/{name}/logs; 'daemon' = /daemon/logs. */
  source?: 'app' | 'daemon'
  /** When false the stream is aborted and not re-opened. */
  active?: boolean
  /** Backlog size; also caps the live buffer while following. */
  tail: number
  /**
   * Viewport follow state. Only affects the CAP (freeze-on-unfollow, see
   * effectiveCap) — the stream itself always keeps running.
   */
  follow?: boolean
  /**
   * While true (user is drag-selecting log text), incoming lines accumulate
   * in the pending buffer and are NOT applied to state — any DOM update would
   * collapse the native selection. Flushed immediately on unpause.
   */
  paused?: boolean
}

/**
 * useLogStream owns the log line buffer and the backlog + live-tail streaming
 * lifecycle for one log source.
 *
 * Streaming model: TWO requests — a complete (closed) non-follow GET replays
 * the last `tail` lines, and a `follow=true&tail=0` stream carries only NEW
 * lines. The split matters for the desktop webview: the Wails/WebKitGTK asset
 * bridge only drains its stream buffer when more bytes arrive or the response
 * closes, so a follow stream that bursts the whole backlog and goes idle
 * silently truncates it. A closed backlog response flushes in full; the follow
 * stream then carries only small live increments. The follow stream is opened
 * FIRST and its lines are held until the backlog lands; the overlap between
 * the two is deduplicated on merge (see releaseHold), so the seam between the
 * backlog snapshot and the live tail loses nothing.
 *
 * The stream reconnects on every non-aborted end — crucially when the daemon
 * closes the follow because the container was recreated (a restarting app gets
 * a NEW container id), or when the app has no container yet (mid-restart).
 *
 * Live chunks are COALESCED: each read lands in a pending array and a single
 * timer applies them in one state update per LOG_FLUSH_INTERVAL_MS. On bursty
 * streams this cuts renders (and the O(n) filter pass each one triggers) by an
 * order of magnitude.
 */
export function useLogStream({ appName, source = 'app', active = true, tail, follow = true, paused = false }: UseLogStreamOptions) {
  const [entries, setEntries] = useState<LogEntry[]>([])
  const abortRef = useRef<AbortController | null>(null)
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Trailing partial line not yet terminated by '\n'. Held out of the buffer
  // so the rendered lines only ever contain COMPLETE lines — no perpetual
  // trailing "" inflating the count (500, not 501) — while still joining
  // lines split across stream reads.
  const pendingRef = useRef('')
  // Complete lines received but not yet applied to state (coalescing window /
  // selection pause). Applied in one setEntries per flush.
  const pendingLinesRef = useRef<string[]>([])
  const flushTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // The buffer is mirrored in a ref so flushes can compute the next array
  // OUTSIDE the setState updater: ids come from a counter ref, and advancing
  // it inside an updater would double-advance under StrictMode's
  // double-invoke. All mutations funnel through commit().
  const bufferRef = useRef<LogEntry[]>([])
  const nextIdRef = useRef(0)
  // Options mirrored into refs so the []-dep stream callbacks see the current
  // values without being recreated (which would restart the stream).
  const tailRef = useRef(tail)
  const activeRef = useRef(active)
  const followRef = useRef(follow)
  const pausedRef = useRef(paused)
  useEffect(() => {
    tailRef.current = tail
    activeRef.current = active
    followRef.current = follow
    pausedRef.current = paused
  }, [tail, active, follow, paused])

  const commit = useCallback((next: LogEntry[]) => {
    bufferRef.current = next
    setEntries(next)
  }, [])

  /** Applies pending complete lines in one state update (unless paused). */
  const flushPending = useCallback(() => {
    if (pausedRef.current) return // unpause effect re-flushes
    const lines = pendingLinesRef.current
    if (lines.length === 0) return
    pendingLinesRef.current = []
    const cap = effectiveCap(tailRef.current, followRef.current)
    const next = appendEntriesToBuffer(bufferRef.current, lines, nextIdRef.current, cap)
    nextIdRef.current += lines.length
    commit(next)
  }, [commit])

  const scheduleFlush = useCallback(() => {
    if (flushTimerRef.current !== null) return
    flushTimerRef.current = setTimeout(() => {
      flushTimerRef.current = null
      flushPending()
    }, LOG_FLUSH_INTERVAL_MS)
  }, [flushPending])

  /** Replaces the buffer with a complete backlog response (or clears it). */
  const setAllLines = useCallback((newLines: string[]) => {
    // Drop the trailing empty element left when a backlog that ends in '\n' is
    // split, so the count matches the tail exactly. Resets the partial-line
    // and pending buffers — the backlog is a complete, self-contained response.
    const trimmed = newLines.slice()
    while (trimmed.length > 0 && trimmed[trimmed.length - 1] === '') trimmed.pop()
    pendingRef.current = ''
    pendingLinesRef.current = []
    const next = makeEntries(trimmed, null, nextIdRef.current)
    nextIdRef.current += trimmed.length
    commit(next)
  }, [commit])

  const clear = useCallback(() => setAllLines([]), [setAllLines])

  const appendChunk = useCallback((chunk: string) => {
    // Buffer the trailing partial line in pendingRef; queue only complete
    // lines. The actual splitting/capping logic is pure (splitChunkLines /
    // appendEntriesToBuffer) so it stays unit-testable.
    const { complete, pending } = splitChunkLines(pendingRef.current, chunk)
    pendingRef.current = pending
    if (complete.length === 0) return
    pendingLinesRef.current.push(...complete)
    // Safety: a firehose during a long selection pause must not grow the
    // pending array unboundedly — older lines would be trimmed anyway.
    if (pendingLinesRef.current.length > MAX_LOG_LINES) {
      pendingLinesRef.current = pendingLinesRef.current.slice(-MAX_LOG_LINES)
    }
    scheduleFlush()
  }, [scheduleFlush])

  // Unpause → apply everything deferred during the selection drag.
  useEffect(() => {
    if (!paused) flushPending()
  }, [paused, flushPending])

  // Follow resumed → re-apply the tail cap the frozen window ignored. The
  // viewport is about to jump to the bottom anyway, so the big front-trim is
  // invisible to the user.
  useEffect(() => {
    if (!follow) return
    const cap = effectiveCap(tailRef.current, true)
    if (bufferRef.current.length > cap) {
      commit(bufferRef.current.slice(-cap))
    }
  }, [follow, commit])

  // Abort streaming when deactivated
  useEffect(() => {
    if (!active) {
      abortRef.current?.abort()
    }
  }, [active])

  // Streaming — live tail attached FIRST, backlog merged over it. The stream
  // is kept open regardless of the caller's follow toggle: follow only
  // controls the buffer cap and whether new chunks auto-scroll the viewport
  // (see LogViewport).
  useEffect(() => {
    if (!appName || !active) return

    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller

    const base = source === 'daemon'
      ? `${API_BASE}/daemon/logs`
      : `${API_BASE}/apps/${encodeURIComponent(appName)}/logs`

    // Seam between the backlog snapshot and the live tail: the follow stream
    // is opened BEFORE the backlog request, and its lines are HELD here until
    // the backlog lands. A line emitted in between is then present in both —
    // releaseHold drops the overlap (overlapLineCount) so nothing is lost and
    // nothing duplicates. The old serial order (backlog first, follow after)
    // silently LOST every line emitted between the two requests.
    const hold = { active: true, lines: [] as string[], pending: '' }
    const releaseHold = (backlogLines: string[] | null) => {
      if (!hold.active) return
      hold.active = false
      let gapLines = hold.lines
      if (backlogLines) {
        gapLines = gapLines.slice(overlapLineCount(backlogLines, gapLines))
      }
      // Hand the held tail over to the normal append pipeline: complete lines
      // via the coalescing buffer, the trailing partial via pendingRef (empty
      // at this point — setAllLines reset it and appendChunk hasn't run yet).
      pendingRef.current = hold.pending
      if (gapLines.length > 0) {
        pendingLinesRef.current.push(...gapLines)
        scheduleFlush()
      }
    }

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
          const text = decoder.decode(value, { stream: true })
          if (hold.active) {
            // Backlog still loading — park complete lines for the seam merge.
            const { complete, pending } = splitChunkLines(hold.pending, text)
            hold.pending = pending
            hold.lines.push(...complete)
            if (hold.lines.length > MAX_LOG_LINES) hold.lines = hold.lines.slice(-MAX_LOG_LINES)
          } else {
            appendChunk(text)
          }
        }
        // Clean end → container was recreated/stopped: resume on the new one.
        reconnect(1000)
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return
        reconnect(3000)
      }
    }

    // Backlog: non-follow GET → a complete, closed response, delivered in full
    // over the asset bridge (a follow stream that bursts the backlog and goes
    // idle is silently truncated by the Wails/WebKitGTK bridge — that is why
    // the backlog is NOT simply requested on the follow stream). Replaces
    // state, then releases the held live lines over it.
    const loadBacklog = async () => {
      try {
        const res = await fetch(`${base}?tail=${tail}`, { signal: controller.signal })
        if (res.ok) {
          const text = await res.text()
          const trimmed = text.split('\n')
          setAllLines(trimmed)
          releaseHold(bufferRef.current.map((e) => e.text))
          return
        }
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return
        // A transient backlog error shouldn't block live tailing — fall through.
      }
      if (controller.signal.aborted) return
      releaseHold(null)
    }

    // Order matters: the follow stream goes out first so it is (near-)always
    // attached on the daemon side before the backlog snapshot is read — that
    // is what makes the hold+dedup seam lossless in practice.
    void startLiveStream()
    void loadBacklog()

    return () => {
      controller.abort()
      if (retryTimerRef.current) {
        clearTimeout(retryTimerRef.current)
        retryTimerRef.current = null
      }
      if (flushTimerRef.current) {
        clearTimeout(flushTimerRef.current)
        flushTimerRef.current = null
      }
    }
  }, [appName, tail, appendChunk, setAllLines, scheduleFlush, active, source])

  return { entries, clear }
}
