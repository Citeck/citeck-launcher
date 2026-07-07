import { useEffect, useState, useRef } from 'react'
import { useLogStream, LOG_LEVELS, LEVEL_COLORS } from '../hooks/useLogStream'
import { useLogFilter } from '../hooks/useLogFilter'
import { LogViewport } from './LogViewport'
import { useTranslation } from '../lib/i18n'
import { copyText } from '../lib/clipboard'
import { primeDesktopModeCache } from '../lib/desktop'
import { saveDownload, openDownloadsFolder } from '../lib/api'
import { toast } from '../lib/toast'

interface LogViewerProps {
  appName: string
  compact?: boolean
  active?: boolean
  /** 'app' (default) = /apps/{name}/logs; 'daemon' = /daemon/logs. */
  source?: 'app' | 'daemon'
}

/**
 * LogViewer composes three pieces:
 *  - useLogStream — owns the line/level buffer + backlog/live-tail streaming
 *  - useLogFilter — level toggles, wildcard filter, search + match navigation
 *  - LogViewport — presentational virtualized list with follow behaviour
 *
 * This component itself only renders the toolbar/status bar and wires the
 * global keyboard shortcuts (Kotlin LogsWindow parity).
 */
export function LogViewer({ appName, compact = false, active = true, source = 'app' }: LogViewerProps) {
  const { t } = useTranslation()
  const [tail, setTail] = useState(500)
  const [follow, setFollow] = useState(true)
  // Kotlin parity (LogsViewer.kt — wordWrap defaulted to false): logs are
  // typically already line-oriented; soft-wrapping every long line by default
  // hides the indentation cues that make stack traces and JSON-payload lines
  // scannable. User can flip "Wrap" on when they want it.
  const [wordWrap, setWordWrap] = useState(false)
  // True while the user is drag-selecting log text — the stream hook defers
  // applying new chunks (a DOM update mid-drag collapses the selection).
  const [selecting, setSelecting] = useState(false)

  const { entries, clear } = useLogStream({ appName, source, active, tail, follow, paused: selecting })
  const {
    search, setSearch,
    filterText, setFilterText,
    useRegex, setUseRegex,
    enabledLevels, toggleLevel,
    filteredEntries, totalLineCount,
    safeSearchRegex, regexWarning,
    matchIndices, safeMatchIndex, setMatchIndex,
    searchNavTick, bumpSearchNav,
  } = useLogFilter(entries)

  // Scroll container — owned here so Ctrl+A can scope its selection to the
  // log viewport; passed down to LogViewport which attaches the virtualizer.
  const parentRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  // Active "select all" marker shared with LogViewport (copy override +
  // re-applied selection visuals live there; Ctrl+A lives here).
  const selectAllRef = useRef(false)
  // Refs to copy/download functions so keyboard shortcuts can fire without
  // adding them to the keydown effect's deps (they capture filteredLines).
  const copyToClipboardRef = useRef<() => void>(undefined)
  const downloadRef = useRef<() => void>(undefined)

  // Keyboard shortcuts — match Kotlin LogsWindow.
  useEffect(() => {
    if (!active) return
    function handleKeyDown(e: KeyboardEvent) {
      const ctrl = e.ctrlKey || e.metaKey
      // Focused-element guard: these are GLOBAL shortcuts, so when any
      // editable element has focus — the search/filter inputs, a dialog
      // field, or CodeMirror's contenteditable DIV — the destructive /
      // override shortcuts (Ctrl+A select-logs, Ctrl+S download, Ctrl+L
      // clear, Ctrl+Shift+C copy, …) must not hijack it. The viewer's own
      // search input is the one exception: its find-bar keys (Ctrl+F
      // re-select, F3/Ctrl+G match nav, Escape close) keep working while
      // typing the query; everything else is left to the input.
      const focused = document.activeElement as HTMLElement | null
      const inEditable = !!focused && (
        focused.tagName === 'INPUT' ||
        focused.tagName === 'TEXTAREA' ||
        focused.isContentEditable
      )
      if (inEditable) {
        if (focused !== searchRef.current) return
        const findBarKey =
          (ctrl && !e.shiftKey && e.code === 'KeyF') ||
          e.key === 'F3' ||
          (ctrl && e.code === 'KeyG') ||
          e.key === 'Escape'
        if (!findBarKey) return
      }
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
        clear()
        return
      }
      // Ctrl+A — select only the log lines, not the whole window. The browser's
      // default select-all grabs the toolbar, status bar and window chrome too;
      // scope the selection to the log viewport instead. (Editable elements,
      // including the search/filter inputs, never reach here — the guard at
      // the top of this handler keeps their native select-all.)
      if (ctrl && !e.shiftKey && e.code === 'KeyA') {
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
        // Also leave select-all mode — otherwise the viewport keeps
        // re-applying the full-container selection on every scroll.
        selectAllRef.current = false
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [clear, active, bumpSearchNav, setMatchIndex, setSearch])

  function copyToClipboard() {
    void copyText(filteredEntries.map((e) => e.text).join('\n'))
  }

  async function downloadLogs() {
    // Kotlin's default filename pattern: "<windowTitle>_<yyyyMMdd_HHmmss>.log"
    const d = new Date()
    const pad = (n: number) => String(n).padStart(2, '0')
    const ts = `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}_${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`
    const filename = source === 'daemon' ? `daemon_${ts}.log` : `${appName}_${ts}.log`
    const content = filteredEntries.map((e) => e.text).join('\n')

    // Desktop: the WebKitGTK webview has no download manager, so <a download> is
    // a no-op. Save server-side into Downloads, then offer to open the folder.
    // AWAIT the probe (not the sync cache): a standalone logs window may not have
    // primed it yet, and a stale `false` would silently fall back to the no-op
    // blob path — exactly the "Download does nothing" bug.
    if (await primeDesktopModeCache()) {
      try {
        await saveDownload(filename, content)
        toast(t('logViewer.download.saved'), 'success', {
          label: t('logViewer.download.openFolder'),
          onClick: () => { void openDownloadsFolder() },
        })
      } catch (e) {
        toast(`${t('logViewer.download.failed')}: ${(e as Error).message}`, 'error')
      }
      return
    }

    // Browser/server mode: the standard blob download works.
    const blob = new Blob([content], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 5000)
  }

  // Latest-ref mirror, updated post-render (refs must not be written during
  // render): the keydown shortcuts call the freshest copy/download closures
  // without listing them in the effect's deps.
  useEffect(() => {
    copyToClipboardRef.current = copyToClipboard
    downloadRef.current = downloadLogs
  })

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

        {/* Toggles — "Follow" is a floating button at the bottom-right of
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
          onClick={clear} title={t('logViewer.clear.tooltip')}>{t('logViewer.clear')}</button>
      </div>

      {/* Log output — virtualized list with stick-to-bottom follow. */}
      <LogViewport
        entries={filteredEntries}
        safeSearchRegex={safeSearchRegex}
        matchIndices={matchIndices}
        safeMatchIndex={safeMatchIndex}
        searchNavTick={searchNavTick}
        wordWrap={wordWrap}
        compact={compact}
        follow={follow}
        setFollow={setFollow}
        parentRef={parentRef}
        selectAllRef={selectAllRef}
        onSelectingChange={setSelecting}
      />

      {/* Status bar */}
      <div className={`flex items-center justify-between text-xs text-muted-foreground ${compact ? 'px-2 py-0.5' : 'mt-2'}`}>
        <span>
          {filteredEntries.length !== totalLineCount
            ? t('logViewer.linesTotal', { count: filteredEntries.length, total: totalLineCount })
            : t('logViewer.lines', { count: filteredEntries.length })}
        </span>
        <span>
          {follow && t('logViewer.streaming')} | {t('logViewer.shortcuts')}
        </span>
      </div>
    </div>
  )
}
