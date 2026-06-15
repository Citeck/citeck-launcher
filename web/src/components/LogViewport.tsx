import { useEffect, useRef } from 'react'
import type { RefObject } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { ArrowDown } from 'lucide-react'
import type { LogLevel } from '../hooks/useLogStream'
import { LEVEL_COLORS } from '../hooks/useLogStream'
import { useTranslation } from '../lib/i18n'

export interface LogViewportProps {
  filteredLines: string[]
  filteredLevels: (LogLevel | null)[]
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
}

/**
 * LogViewport is the presentational virtual-list half of the log viewer:
 * virtualized rendering, stick-to-bottom follow behaviour, search-match
 * scrolling, select-all visuals and the floating "Follow" button. It owns no
 * data state — lines/levels/search all arrive as props from useLogStream +
 * useLogFilter via LogViewer.
 */
export function LogViewport({
  filteredLines,
  filteredLevels,
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
}: LogViewportProps) {
  const { t } = useTranslation()
  // When we programmatically scroll (auto-stick-to-bottom, search recentre,
  // initial backlog landing), the browser still fires onScroll. Without this
  // gate the handler reads "scrollTop=0 + tall content" during the very
  // first paint and immediately disables follow — leaving the new window
  // pinned at the top instead of at the live tail. Pulse-high during the
  // animation frame following a programmatic scroll so the onScroll handler
  // can tell user input from layout-driven scrolling.
  const programmaticScrollRef = useRef(false)
  const followRef = useRef(follow)
  followRef.current = follow

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
  }, [renderedRangeKey, selectAllRef, parentRef])

  // The log list is virtualized, so a native Ctrl+A selection only spans the
  // rendered rows. The copy override below hands over the FULL filtered text;
  // the ref mirrors filteredLines for that synchronous copy-event read.
  const filteredLinesRef = useRef(filteredLines)
  filteredLinesRef.current = filteredLines

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
  }, [selectAllRef, parentRef])

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
