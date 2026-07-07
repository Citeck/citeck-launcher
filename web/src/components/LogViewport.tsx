import { useEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { ArrowDown } from 'lucide-react'
import type { LogEntry } from '../hooks/useLogStream'
import { LEVEL_COLORS } from '../hooks/useLogStream'
import type { DragSelection } from '../lib/logSelection'
import { buildDragSelectionText, pickRowIndexAtY, resolveRowEndpoint } from '../lib/logSelection'
import { useTranslation } from '../lib/i18n'

export interface LogViewportProps {
  /** Filtered entries; ids are the stable virtualizer row keys. */
  entries: LogEntry[]
  safeSearchRegex: RegExp | null
  matchIndices: number[]
  safeMatchIndex: number
  /** Bumped by the filter hook on explicit search navigation → scroll to match. */
  searchNavTick: number
  wordWrap: boolean
  compact: boolean
  follow: boolean
  setFollow: (follow: boolean) => void
  /**
   * Scroll container ref, owned by the parent so its keyboard shortcuts
   * (Ctrl+A select-all scoping) can reach the same element.
   */
  parentRef: RefObject<HTMLDivElement | null>
  /** Shared "select all is active" flag (set by the parent's Ctrl+A handler). */
  selectAllRef: RefObject<boolean>
  /**
   * Fired when the user starts/stops a mouse drag on the log content. The
   * parent pauses applying new stream chunks while true — any DOM update
   * during the drag would collapse the native text selection.
   */
  onSelectingChange: (selecting: boolean) => void
}

/**
 * LogViewport is the presentational virtual-list half of the log viewer:
 * virtualized rendering, stick-to-bottom follow behaviour, search-match
 * scrolling, select-all visuals and the floating "Follow" button. It owns no
 * data state — entries/search all arrive as props from useLogStream +
 * useLogFilter via LogViewer.
 *
 * Follow model: BREAKING follow happens on user intent (wheel-up — see
 * onWheel), which a programmatic scroll can never emit, so it cannot be eaten
 * by the programmatic-scroll gate no matter how fast chunks arrive. The
 * scroll-position heuristic in onScroll remains for scrollbar/keyboard
 * scrolling and for RE-ARMING follow at the bottom.
 */
export function LogViewport({
  entries,
  safeSearchRegex,
  matchIndices,
  safeMatchIndex,
  searchNavTick,
  wordWrap,
  compact,
  follow,
  setFollow,
  parentRef,
  selectAllRef,
  onSelectingChange,
}: LogViewportProps) {
  const { t } = useTranslation()
  // When we programmatically scroll (auto-stick-to-bottom, search recentre,
  // initial backlog landing), the browser still fires onScroll. Without this
  // gate the handler reads "scrollTop=0 + tall content" during the very
  // first paint and immediately disables follow — leaving the new window
  // pinned at the top instead of at the live tail. Pulse-high during the
  // animation frames following a programmatic scroll so the onScroll handler
  // can tell user input from layout-driven scrolling.
  const programmaticScrollRef = useRef(false)
  const followRef = useRef(follow)
  followRef.current = follow

  // Fixed-row fast path: with wrap OFF every row is exactly one text line
  // high, so per-row dynamic measurement is skipped entirely — no
  // ResizeObservers and no scroll-offset corrections while scrolling up
  // through unmeasured rows (the measure/adjust storm read as "stuttering
  // scroll" a third of the way up a big log). The real row height is probed
  // once from the first rendered row (font/zoom dependent); wrap ON switches
  // back to per-row measureElement for variable heights.
  const [probedRowHeight, setProbedRowHeight] = useState<number | null>(null)
  useEffect(() => {
    if (probedRowHeight !== null) return
    const el = parentRef.current?.querySelector('[data-index]')
    const h = el?.getBoundingClientRect().height ?? 0
    if (h > 0) setProbedRowHeight(h)
    // Re-probe as the list fills: the first renders may have no rows (empty
    // buffer / hidden tab with zero size) — retry until a row measures > 0.
  }, [probedRowHeight, parentRef, entries.length])

  // eslint-disable-next-line react-hooks/incompatible-library -- useVirtualizer returns are consumed locally, no stale UI risk
  const virtualizer = useVirtualizer({
    count: entries.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => probedRowHeight ?? 20,
    overscan: 30,
    // Key rows by the entry's monotonic id, NOT the buffer index. When the
    // sliding window trims old lines off the front, indices shift but ids
    // don't — React nodes and the measurement cache follow the LINE, so the
    // content under the viewport doesn't jump and text selection survives.
    getItemKey: (i) => entries[i]?.id ?? i,
  })
  // When a row above the viewport is (re)measured to a different size while
  // the user is reading (not following), compensate the scroll offset so the
  // visible content doesn't shift — kills the upward-scroll jitter of lazy
  // dynamic measurement. (Instance property, not a constructor option, in
  // this virtual-core version.)
  virtualizer.shouldAdjustScrollPositionOnItemSizeChange = (item, _delta, instance) =>
    !followRef.current && item.start < (instance.scrollOffset ?? 0)

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
  }, [renderedRangeKey, selectAllRef, parentRef])

  // Mirrors `entries` for synchronous reads from the copy / selectionchange
  // event handlers below (registered once, must see the latest list).
  const entriesRef = useRef(entries)
  entriesRef.current = entries

  // The native selection only spans MOUNTED rows: rows scrolled past the
  // overscan window unmount and silently drop out of it, so a copy served
  // from the DOM would contain just the visible slice of a long drag
  // selection. Track the drag's logical bounds — entry id + char offset —
  // which survive unmounting; the copy handler rebuilds the full text from
  // the entry buffer. The anchor is pinned at drag start (the browser
  // re-anchors when the original anchor row unmounts), the focus follows
  // every selectionchange (it sits under the pointer, always mounted).
  const dragSelRef = useRef<DragSelection | null>(null)
  const draggingRef = useRef(false)

  // Select-all must not outlive the selection itself: once the browser
  // selection collapses or leaves the viewport (click on the toolbar, Esc,
  // focus elsewhere), drop the flag. Without this the re-apply effect above
  // kept re-selecting the whole container on EVERY scrolled range change
  // forever — the "scrolling suddenly becomes slow" bug.
  useEffect(() => {
    const onSelectionChange = () => {
      const sel = window.getSelection()
      const lost = !sel || sel.rangeCount === 0 || sel.isCollapsed || !parentRef.current?.contains(sel.anchorNode)
      if (selectAllRef.current && lost) {
        selectAllRef.current = false
      }
      // Drag-bounds tracking. NOT invalidated here on collapse: engines
      // collapse the DOM selection when selected rows unmount (WebKit does it
      // aggressively), and the whole point of the tracked bounds is to
      // survive that. Invalidation is driven by user actions instead —
      // mousedown outside the viewer, Escape, select-all, a fresh drag.
      if (!draggingRef.current) {
        if (selectAllRef.current) dragSelRef.current = null
        return
      }
      const c = parentRef.current
      if (lost || !c || !sel) return
      const anchor = sel.anchorNode ? resolveRowEndpoint(c, sel.anchorNode, sel.anchorOffset) : null
      const idAt = (p: { index: number; offset: number }) => entriesRef.current[p.index]?.id
      if (dragSelRef.current === null && anchor !== null && idAt(anchor) !== undefined) {
        const id = idAt(anchor)!
        dragSelRef.current = { anchorId: id, anchorOffset: anchor.offset, focusId: id, focusOffset: anchor.offset }
      }
      // Clamp BEFORE tracking the focus: when the engine's hit-test lands the
      // focus rows away from the pointer (WebKitGTK, incl. its drag-autoscroll
      // timer that runs with NO mouse events once the pointer crosses the
      // container edge), re-extend and let the induced selectionchange track
      // the corrected focus — the bogus one must never reach the copy bounds.
      if (clampDragFocusRef.current()) return
      const focus = sel.focusNode ? resolveRowEndpoint(c, sel.focusNode, sel.focusOffset) : null
      if (dragSelRef.current !== null && focus !== null && idAt(focus) !== undefined) {
        dragSelRef.current = { ...dragSelRef.current, focusId: idAt(focus)!, focusOffset: focus.offset }
      }
    }
    document.addEventListener('selectionchange', onSelectionChange)
    return () => document.removeEventListener('selectionchange', onSelectionChange)
  }, [selectAllRef, parentRef])

  // Copy override — the list is virtualized, so a DOM-served copy only ever
  // contains the mounted rows. Two cases hand over buffer-built text instead:
  // Ctrl+A select-all (every filtered line) and a tracked drag selection
  // (exact logical range incl. rows that unmounted mid-drag).
  useEffect(() => {
    const onCopy = (e: ClipboardEvent) => {
      // Never hijack a copy from an editable element (search/filter inputs,
      // dialog fields): the document selection may still hold a stale log
      // range while the user is copying the input's own text.
      const focused = document.activeElement as HTMLElement | null
      if (focused && (focused.tagName === 'INPUT' || focused.tagName === 'TEXTAREA' || focused.isContentEditable)) return
      const sel = window.getSelection()
      if (selectAllRef.current) {
        if (!sel || sel.rangeCount === 0 || !parentRef.current?.contains(sel.anchorNode)) return
        e.preventDefault()
        e.clipboardData?.setData('text/plain', entriesRef.current.map((en) => en.text).join('\n'))
        return
      }
      const ds = dragSelRef.current
      if (!ds) return
      // A live selection belonging to ANOTHER part of the page wins over the
      // tracked log range. A collapsed/empty selection does NOT invalidate
      // it — engines collapse the DOM selection when selected rows unmount —
      // and neither does an anchor DETACHED by a row update (isConnected
      // false ≠ "anchored elsewhere"); both are exactly the cases the
      // tracked bounds exist for.
      if (
        sel && !sel.isCollapsed && sel.anchorNode && sel.anchorNode.isConnected &&
        !parentRef.current?.contains(sel.anchorNode)
      ) return
      const text = buildDragSelectionText(entriesRef.current, ds)
      if (text === null || text === '') return
      e.preventDefault()
      e.clipboardData?.setData('text/plain', text)
    }
    document.addEventListener('copy', onCopy)
    return () => document.removeEventListener('copy', onCopy)
  }, [selectAllRef, parentRef])

  // Refs so the layout effects don't have to list `virtualizer` (a fresh
  // instance every render) or `matchIndices` (a fresh array every chunk
  // arrival) in their dep arrays — without these, the scroll-to-index calls
  // re-fired on every render and yanked the viewport away from a user who
  // had scrolled up to read older lines.
  const virtualizerRef = useRef(virtualizer)
  virtualizerRef.current = virtualizer

  // If the viewport unmounts mid-drag (tab close, window switch), the
  // select-none guard must not linger on <body>.
  useEffect(() => () => { document.body.classList.remove('log-select-drag') }, [])

  // Tracked-selection invalidation by user intent: a mousedown anywhere
  // OUTSIDE the viewer, or Escape, ends the logical drag selection. Clicks
  // inside the viewer are handled by the content mousedown itself (fresh
  // drag resets, scrollbar clicks must keep the selection alive).
  useEffect(() => {
    const onDocMouseDown = (ev: MouseEvent) => {
      // Left button only — a right-click means "context menu", and its Copy
      // must still see the tracked selection.
      if (ev.button !== 0) return
      if (draggingRef.current) return
      if (parentRef.current?.contains(ev.target as Node)) return
      dragSelRef.current = null
    }
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === 'Escape') dragSelRef.current = null
    }
    document.addEventListener('mousedown', onDocMouseDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onDocMouseDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [parentRef])

  // Last known pointer Y of the active drag. Updated by mousemove; consumed
  // by the selectionchange clamp below — the clamp must NOT hang off
  // mousemove alone, because when the pointer crosses the container edge the
  // engine keeps extending the selection from its own drag-autoscroll TIMER
  // with no mouse events in between.
  const lastPointerYRef = useRef<number | null>(null)

  // Engine-bug safety net for drag-selection: WebKitGTK's caret hit-test can
  // resolve a point in the empty space right of a short line (a blank row,
  // or anywhere past the container's right edge) to the FIRST position of
  // the wrapper — the selection visually jumps to the top of the list.
  // Compare the row the ENGINE put the focus in with the row actually under
  // the pointer; if they disagree by more than one row, re-extend the
  // selection to the end of the pointer's row. On correct engines the
  // mismatch never trips, so this stays dormant. Returns true when it
  // corrected the selection (the caller then waits for the induced
  // selectionchange instead of tracking the bogus focus).
  function clampDragFocus(): boolean {
    const pointerY = lastPointerYRef.current
    const c = parentRef.current
    if (pointerY === null || !c) return false
    const sel = window.getSelection()
    if (!sel || sel.rangeCount === 0) return false
    const rowEls = Array.from(c.querySelectorAll<HTMLElement>('[data-index]'))
    const bands = rowEls.map((r) => {
      const b = r.getBoundingClientRect()
      return { index: Number(r.getAttribute('data-index')), top: b.top, bottom: b.bottom }
    })
    // Clamp the pointer into the container box: above the top edge targets the
    // first visible row (native auto-scroll keeps feeding rows in).
    const cb = c.getBoundingClientRect()
    const y = Math.min(Math.max(pointerY, cb.top + 1), cb.bottom - 1)
    const target = pickRowIndexAtY(bands, y)
    if (target === null) return false
    const focus = sel.focusNode ? resolveRowEndpoint(c, sel.focusNode, sel.focusOffset) : null
    if (focus !== null && Math.abs(focus.index - target) <= 1) return false
    const rowEl = rowEls.find((r) => Number(r.getAttribute('data-index')) === target)
    if (!rowEl) return false
    try {
      sel.extend(rowEl, rowEl.childNodes.length)
      return true
    } catch {
      // extend() throws on nodes detached by a concurrent row update — the
      // next selection change retries with fresh nodes.
      return false
    }
  }
  const clampDragFocusRef = useRef(clampDragFocus)
  clampDragFocusRef.current = clampDragFocus

  // Wrap toggle / probe arrival invalidate every cached measurement: wrap-on
  // heights are meaningless after wrap-off (and vice versa), and the probed
  // height replaces the initial 20px estimate for all unmeasured rows.
  useEffect(() => {
    virtualizerRef.current.measure()
  }, [wordWrap, probedRowHeight])
  const matchIndicesRef = useRef(matchIndices)
  matchIndicesRef.current = matchIndices
  // safeMatchIndexRef mirrors safeMatchIndex so the scroll effect can read it
  // without making it a dependency (which would re-fire on every chunk arrival
  // that shifts matchIndices, hard-pinning the viewport and blocking free scroll).
  const safeMatchIndexRef = useRef(safeMatchIndex)
  safeMatchIndexRef.current = safeMatchIndex

  function programmaticScrollTo(idx: number, align: 'start' | 'center' | 'end') {
    programmaticScrollRef.current = true
    virtualizerRef.current.scrollToIndex(idx, { align })
    // Second pass one frame later: rows measured after the first scroll can
    // shift the target offset (estimate 20px vs real height), leaving an
    // 'end' align short of the actual bottom. Re-issue with fresh measurements,
    // then clear the gate so the onScroll events emitted by both calls have
    // had time to run + be ignored.
    requestAnimationFrame(() => {
      virtualizerRef.current.scrollToIndex(idx, { align })
      requestAnimationFrame(() => { programmaticScrollRef.current = false })
    })
  }

  // Resume following: jump to the newest line and re-arm follow. Fired by the
  // floating button.
  function resumeFollow() {
    setFollow(true)
    const len = entries.length
    if (len > 0) programmaticScrollTo(len - 1, 'end')
  }

  // Auto-scroll-to-bottom: only when follow is on AND the newest line actually
  // changed. Tracked by the tail entry's ID, not the array length — at the
  // buffer cap the length is constant (trim N + append N) while the content
  // still advances. Plain re-renders (e.g. virtualizer re-measuring an item
  // in view) don't trigger a scroll.
  const lastEntryId = entries.length > 0 ? entries[entries.length - 1].id : -1
  const prevFollowedIdRef = useRef(lastEntryId)
  useEffect(() => {
    if (follow && lastEntryId >= 0 && lastEntryId !== prevFollowedIdRef.current) {
      programmaticScrollTo(entriesRef.current.length - 1, 'end')
    }
    prevFollowedIdRef.current = lastEntryId
  }, [lastEntryId, follow])

  // Keep the newest line pinned to the bottom while following when the viewport
  // resizes (window resize, panel drag, wrap toggle changing row heights).
  // A size change moves the scroll bottom without firing onScroll, so without
  // this the view would drift off the tail.
  useEffect(() => {
    const el = parentRef.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(() => {
      if (!followRef.current) return
      const len = entriesRef.current.length
      if (len === 0) return
      programmaticScrollRef.current = true
      virtualizerRef.current.scrollToIndex(len - 1, { align: 'end' })
      requestAnimationFrame(() => {
        requestAnimationFrame(() => { programmaticScrollRef.current = false })
      })
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [parentRef])

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

  return (
    <div className={`relative flex-1 min-h-0 ${compact ? 'mx-2 mb-1' : ''}`}>
      <div
        ref={parentRef}
        // select-text keeps this subtree selectable while the body-level
        // `log-select-drag` guard (see onMouseDown below) suppresses selection
        // everywhere else during a drag.
        className={`h-full w-full select-text overflow-auto rounded-lg border border-border bg-card p-4 font-mono text-xs ${wordWrap ? '' : 'overflow-x-auto'}`}
        // Wheel-up is unambiguous user intent to stop following. Unlike the
        // onScroll heuristic it cannot be swallowed by the programmatic-scroll
        // gate, which on a bursty stream is high a large fraction of the time
        // (every chunk pulses it) — the old "can't unstick from the bottom" bug.
        onWheel={(e) => {
          if (e.deltaY < 0 && followRef.current) setFollow(false)
        }}
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
          // ~50px (scrollbar drag / keyboard — wheel is handled above),
          // re-arm it when they scroll back to the bottom.
          const distFromBottom = scrollHeight - scrollTop - clientHeight
          if (distFromBottom > 50) {
            if (followRef.current) setFollow(false)
          } else {
            if (!followRef.current) setFollow(true)
          }
        }}
      >
        {entries.length === 0 ? (
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
            // just the user's range, and signal the drag so the parent pauses
            // stream flushes (a DOM update mid-drag collapses the selection).
            // The body-level guard class turns off user-select everywhere
            // OUTSIDE the viewport for the duration of the drag — without it,
            // dragging above the viewport pulls the toolbar and window chrome
            // into the selection. Scrollbar drags land on the parent, not
            // here, so scrolling keeps select-all alive and doesn't pause the
            // stream.
            onMouseDown={(e) => {
              // LEFT button only. A right-click opens the context menu and
              // its mouseup never reaches us — a "drag" started here would
              // stay armed forever: the selection then follows the bare
              // cursor and the per-selectionchange clamp work makes wheel
              // scrolling crawl. The click also must not touch the existing
              // selection state (context-menu Copy needs it intact).
              if (e.button !== 0) return
              selectAllRef.current = false
              // Fresh drag → new logical anchor (set on the first
              // selectionchange). Shift+click EXTENDS the browser selection
              // from the existing anchor, so keep the tracked one then.
              if (!e.shiftKey || dragSelRef.current === null) dragSelRef.current = null
              draggingRef.current = true
              lastPointerYRef.current = e.clientY
              onSelectingChange(true)
              document.body.classList.add('log-select-drag')
              // mousemove only RECORDS the pointer — the clamp itself runs on
              // selectionchange, which also catches engine-timer updates that
              // arrive without any mouse event. It must NOT infer "drag over"
              // from ev.buttons: the browser synthesizes mousemoves when
              // content scrolls under a held wheel, and those can carry a
              // stale buttons=0.
              const onMove = (ev: MouseEvent) => { lastPointerYRef.current = ev.clientY }
              const endDrag = () => {
                window.removeEventListener('mousemove', onMove)
                window.removeEventListener('mouseup', endDrag)
                window.removeEventListener('blur', endDrag)
                document.removeEventListener('visibilitychange', endDrag)
                draggingRef.current = false
                document.body.classList.remove('log-select-drag')
                onSelectingChange(false)
              }
              window.addEventListener('mousemove', onMove)
              window.addEventListener('mouseup', endDrag)
              // Missed-mouseup safety nets: releasing the button outside a
              // lost/hidden window never delivers mouseup — a stuck drag
              // would make the selection follow the bare cursor forever.
              window.addEventListener('blur', endDrag)
              document.addEventListener('visibilitychange', endDrag)
            }}
          >
            {renderedItems.map((virtualItem) => {
              const idx = virtualItem.index
              const entry = entries[idx]
              const colorClass = entry.level ? LEVEL_COLORS[entry.level] : 'text-foreground'
              const isCurrentMatch = matchIndices[safeMatchIndex] === idx

              return (
                <div
                  key={virtualItem.key}
                  data-index={idx}
                  // Measure rows only when wrap can make their heights vary;
                  // wrap-off rows are uniform (see probedRowHeight above).
                  ref={wordWrap ? virtualizer.measureElement : undefined}
                  // Positioned via `top`, NOT `transform: translateY` — with
                  // transforms every row sits at layout-y 0 and only the
                  // visual position differs, and WebKitGTK's caret hit-test
                  // (positionForPoint) is transform-unaware in places, which
                  // broke drag-selection in the desktop webview.
                  style={{
                    position: 'absolute',
                    top: `${virtualItem.start}px`,
                    left: 0,
                    width: '100%',
                  }}
                  className={`${colorClass} ${wordWrap ? 'whitespace-pre-wrap break-all' : 'whitespace-pre'} ${isCurrentMatch ? 'bg-primary/10' : ''}`}
                >
                  {/* A blank log line must still produce a line box with a
                      caret position — an empty div has neither, and WebKit's
                      hit-test then escalates to the wrapper. The NBSP is
                      visual only; copies are built from the buffer text. */}
                  {entry.text === ''
                    ? ' '
                    : safeSearchRegex
                      ? highlightSearch(entry.text, safeSearchRegex, isCurrentMatch)
                      : entry.text}
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
          className="absolute bottom-3 right-4 z-10 flex items-center gap-1 rounded-full border border-border bg-card px-3 py-1.5 text-xs font-medium text-primary shadow-md hover:bg-muted"
        >
          <ArrowDown size={14} />
          {t('logViewer.follow')}
        </button>
      )}
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
