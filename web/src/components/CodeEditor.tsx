import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import CodeMirror, { type Extension, EditorState, EditorView } from '@uiw/react-codemirror'
import { Search, ChevronUp, ChevronDown, X, CaseSensitive, Regex } from 'lucide-react'
import { useTranslation } from '../lib/i18n'
import { useThemeStore } from '../lib/theme'
import { searchHighlighter, computeMatches, setSearchMatches, type Match } from './editorSearch'
import { yaml } from '@codemirror/lang-yaml'
import { json } from '@codemirror/lang-json'
import { javascript } from '@codemirror/lang-javascript'
import { shell } from '@codemirror/legacy-modes/mode/shell'
import { dockerFile } from '@codemirror/legacy-modes/mode/dockerfile'
import { lua } from '@codemirror/legacy-modes/mode/lua'
import { clike } from '@codemirror/legacy-modes/mode/clike'
// StreamLanguage isn't re-exported from `@uiw/react-codemirror`, so pull it
// from the canonical `@codemirror/language` package. The lock-file already
// pins this via the legacy-modes peer chain.
import { indentUnit, StreamLanguage } from '@codemirror/language'
import { undo, redo } from '@codemirror/commands'
import { changeGutterExtension, setBaseline } from './changeGutter'

interface CodeEditorProps {
  value: string
  onChange?: (v: string) => void
  /** When true, render read-only (no edits, hide cursor). */
  readOnly?: boolean
  /** Filename or extension hint for syntax highlighting. */
  filename?: string
  /** Editor height in CSS units (e.g. "300px"). Default fills parent. */
  height?: string
  /** Auto-focus on mount — useful for editor windows. */
  autoFocus?: boolean
  /**
   * Generated baseline to diff against for the left change gutter. When set
   * (even ""), a git-style gutter marks lines changed/added vs the baseline.
   */
  baseline?: string
}

/**
 * Thin wrapper around CodeMirror 6 with syntax highlighting driven by file
 * extension. Matches Kotlin `EditorWindow` language table:
 *   yml/yaml → YAML, kt/java → C-like, js → JavaScript, json → JSON,
 *   lua → Lua, Dockerfile → Dockerfile, sh → Unix shell, anything else → plain.
 *
 * Ctrl+F opens a custom search toolbar (top-right) with match count,
 * prev/next, case-sensitive and regex toggles — replacing CodeMirror's stock
 * bottom panel, which mounted with a visible delay and looked out of place.
 */
export function CodeEditor({ value, onChange, readOnly = false, filename = '', height, autoFocus = false, baseline }: CodeEditorProps) {
  const { t } = useTranslation()
  // Follow the app theme — a hardcoded dark editor looked wrong inside the
  // light-theme config dialog. Reactive in the main app; in a secondary
  // editor window the store seeds from the same persisted localStorage value
  // useInheritedTheme reads, so it opens with the right theme.
  const isDark = useThemeStore((s) => s.isDark)
  const viewRef = useRef<EditorView | null>(null)
  const inputRef = useRef<HTMLInputElement | null>(null)
  const [searchOpen, setSearchOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [caseSensitive, setCaseSensitive] = useState(false)
  const [regexp, setRegexp] = useState(false)
  const [matches, setMatches] = useState<Match[]>([])
  const [current, setCurrent] = useState(-1)
  // Presence (not value) of baseline gates the gutter extension; the baseline
  // text itself is pushed reactively via setBaseline so it must NOT rebuild the
  // editor on every change.
  const hasBaseline = baseline !== undefined
  const revertLabel = t('appConfig.revertLine')

  const extensions = useMemo<Extension[]>(() => {
    const lang = detectLanguage(filename)
    const exts: Extension[] = []
    if (lang) exts.push(lang)
    // 2-space tabs everywhere — Kotlin EditorWindow / our YAML conventions.
    // tabSize.of controls how a literal tab character renders, indentUnit.of
    // controls what gets inserted when the user invokes indent.
    exts.push(EditorState.tabSize.of(2))
    exts.push(indentUnit.of('  '))
    // Our custom search-match highlighting (driven by the toolbar below).
    exts.push(searchHighlighter())
    // Left change gutter: mark lines changed/added vs the generated baseline,
    // click a marker to revert that line.
    if (hasBaseline) exts.push(...changeGutterExtension(revertLabel))
    // Paint the editor surface on the full parent rectangle even when the
    // file is short. @uiw/react-codemirror's outer wrapper takes the `height`
    // prop, but the inner cm-editor / cm-scroller / cm-content default to
    // content-sized layout — so a 19-line yaml inside a 700px window leaves
    // a 500px dark band the user has to stare at. Force the inner stack to
    // flex-fill its wrapper.
    exts.push(EditorView.theme({
      '&': {
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
      },
      '.cm-scroller': {
        flex: '1 1 auto',
        minHeight: '0',
      },
      '.cm-content': { minHeight: '100%' },
      '.cm-gutters': { minHeight: '100%' },
    }))
    return exts
  }, [filename, hasBaseline, revertLabel])

  // Push baseline updates into the live editor without rebuilding it.
  useEffect(() => {
    const view = viewRef.current
    if (view && baseline !== undefined) {
      view.dispatch({ effects: setBaseline.of(baseline) })
    }
  }, [baseline])

  // Jump the editor selection + viewport to a given match and repaint the
  // active-match decoration. Used both on query change and prev/next nav.
  const focusMatch = useCallback((ms: Match[], idx: number) => {
    const view = viewRef.current
    if (!view) return
    const mark = setSearchMatches.of({ matches: ms, current: idx })
    if (idx >= 0 && idx < ms.length) {
      // Array literal (not push onto a pre-typed array) so TS infers the union
      // of effect types — scrollIntoView's effect differs from our mark effect.
      view.dispatch({
        selection: { anchor: ms[idx].from, head: ms[idx].to },
        effects: [mark, EditorView.scrollIntoView(ms[idx].from, { y: 'center' })],
      })
    } else {
      view.dispatch({ effects: mark })
    }
  }, [])

  // Recompute matches for the current query/options. `jump` moves the
  // selection to the nearest match (on query/option change); on plain doc
  // edits we keep the cursor where it is and only refresh the highlights.
  const runSearch = useCallback((jump: boolean) => {
    const view = viewRef.current
    if (!view) return
    const ms = computeMatches(view.state.doc.toString(), query, { caseSensitive, regexp })
    let idx = -1
    if (ms.length > 0) {
      if (jump) {
        const head = view.state.selection.main.head
        idx = ms.findIndex((m) => m.from >= head)
        if (idx === -1) idx = 0
      } else {
        idx = Math.min(current < 0 ? 0 : current, ms.length - 1)
      }
    }
    setMatches(ms)
    setCurrent(idx)
    focusMatch(ms, jump ? idx : -1) // don't yank the viewport on background edits
  }, [query, caseSensitive, regexp, current, focusMatch])

  // Re-run when the query or its options change (and on open).
  useEffect(() => {
    if (searchOpen) runSearch(true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, caseSensitive, regexp, searchOpen])

  // Keep highlights aligned when the document changes under an open search.
  useEffect(() => {
    if (searchOpen && query) runSearch(false)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value])

  const go = useCallback((delta: number) => {
    if (matches.length === 0) return
    const base = current < 0 ? 0 : current
    const idx = (base + delta + matches.length) % matches.length
    setCurrent(idx)
    focusMatch(matches, idx)
  }, [matches, current, focusMatch])

  const openSearch = useCallback(() => {
    setSearchOpen(true)
    requestAnimationFrame(() => inputRef.current?.select())
  }, [])

  const closeSearch = useCallback(() => {
    setSearchOpen(false)
    setMatches([])
    setCurrent(-1)
    const view = viewRef.current
    if (view) {
      view.dispatch({ effects: setSearchMatches.of({ matches: [], current: -1 }) })
      view.focus()
    }
  }, [])

  // Keyboard shortcuts are matched by PHYSICAL key (event.code), not the
  // produced character — so they work on non-Latin layouts too (on a Russian
  // layout Ctrl+Z emits "я", Ctrl+F emits "а"; matching e.key would silently
  // break undo/search there). Undo/redo are handled here (CM's historyKeymap is
  // disabled below to avoid a double-undo) so they fire regardless of which part
  // of the editor wrapper holds focus — e.g. right after a gutter click-revert.
  const onWrapperKeyDown = useCallback((e: React.KeyboardEvent) => {
    const mod = e.ctrlKey || e.metaKey
    if (mod && e.code === 'KeyF') {
      e.preventDefault()
      e.stopPropagation()
      openSearch()
    } else if (e.key === 'Escape' && searchOpen) {
      e.preventDefault()
      closeSearch()
    } else if (mod && !readOnly && viewRef.current) {
      if (e.code === 'KeyZ' && !e.shiftKey) {
        e.preventDefault()
        undo(viewRef.current)
      } else if ((e.code === 'KeyZ' && e.shiftKey) || e.code === 'KeyY') {
        e.preventDefault()
        redo(viewRef.current)
      }
    }
  }, [searchOpen, openSearch, closeSearch, readOnly])

  return (
    <div className="relative h-full" onKeyDownCapture={onWrapperKeyDown}>
      {searchOpen && (
        <div className="absolute top-2 right-4 z-10 flex items-center gap-0.5 rounded-md border border-border bg-card px-1.5 py-1 shadow-lg">
          <Search size={13} className="ml-0.5 mr-1 shrink-0 text-muted-foreground" />
          <input
            ref={inputRef}
            value={query}
            spellCheck={false}
            placeholder={t('editor.search.placeholder')}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') { e.preventDefault(); go(e.shiftKey ? -1 : 1) }
              else if (e.key === 'Escape') { e.preventDefault(); closeSearch() }
            }}
            className="w-44 bg-transparent text-xs text-foreground outline-none placeholder:text-muted-foreground"
          />
          <span className="mx-1 min-w-[3.5rem] text-center text-[11px] tabular-nums text-muted-foreground">
            {matches.length > 0 ? `${current + 1}/${matches.length}` : (query ? t('editor.search.noResults') : '')}
          </span>
          <button
            type="button"
            title={t('editor.search.prev')}
            disabled={matches.length === 0}
            onClick={() => go(-1)}
            className="rounded p-1 text-muted-foreground enabled:hover:bg-muted enabled:hover:text-foreground disabled:opacity-40"
          >
            <ChevronUp size={14} />
          </button>
          <button
            type="button"
            title={t('editor.search.next')}
            disabled={matches.length === 0}
            onClick={() => go(1)}
            className="rounded p-1 text-muted-foreground enabled:hover:bg-muted enabled:hover:text-foreground disabled:opacity-40"
          >
            <ChevronDown size={14} />
          </button>
          <button
            type="button"
            title={t('editor.search.caseSensitive')}
            aria-pressed={caseSensitive}
            onClick={() => setCaseSensitive((v) => !v)}
            className={`rounded p-1 hover:bg-muted ${caseSensitive ? 'text-primary' : 'text-muted-foreground'}`}
          >
            <CaseSensitive size={14} />
          </button>
          <button
            type="button"
            title={t('editor.search.regex')}
            aria-pressed={regexp}
            onClick={() => setRegexp((v) => !v)}
            className={`rounded p-1 hover:bg-muted ${regexp ? 'text-primary' : 'text-muted-foreground'}`}
          >
            <Regex size={14} />
          </button>
          <button
            type="button"
            title={t('common.close')}
            onClick={closeSearch}
            className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            <X size={14} />
          </button>
        </div>
      )}
      <CodeMirror
        value={value}
        height={height}
        // Wrapper className: also force h-full on @uiw/react-codemirror's
        // own outer div — its `height` prop sets a style on the wrapper, but
        // h-full guarantees the inline style honours 100% even when the prop
        // is omitted by a caller that just wants the editor to fill its
        // flex parent.
        className="h-full"
        extensions={extensions}
        onChange={(v) => onChange?.(v)}
        onCreateEditor={(view) => {
          viewRef.current = view
          // Seed the baseline immediately on (re)creation so the change gutter
          // never diffs against an empty baseline (the [baseline] effect only
          // fires on prop change, not on editor recreation).
          if (baseline !== undefined && baseline !== '') view.dispatch({ effects: setBaseline.of(baseline) })
        }}
        editable={!readOnly}
        autoFocus={autoFocus}
        theme={isDark ? 'dark' : 'light'}
        basicSetup={{
          lineNumbers: true,
          highlightActiveLine: !readOnly,
          // Our own Ctrl+F drives the toolbar above; disable CM's stock search
          // keymap so it doesn't also mount its (laggy, bottom-anchored) panel.
          searchKeymap: false,
          // Undo/redo handled by onWrapperKeyDown (by physical key, so it works
          // on non-Latin layouts); disable CM's key-based historyKeymap to avoid
          // a double-undo. The history STATE (basicSetup `history`) stays on.
          historyKeymap: false,
          foldGutter: true,
          autocompletion: false, // Kotlin EditorWindow has no autocomplete
        }}
      />
    </div>
  )
}

function detectLanguage(filename: string): Extension | null {
  const lower = filename.toLowerCase()
  if (lower.endsWith('.yml') || lower.endsWith('.yaml')) return yaml()
  if (lower.endsWith('.json')) return json()
  if (lower.endsWith('.js') || lower.endsWith('.mjs') || lower.endsWith('.cjs')) return javascript()
  if (lower.endsWith('.kt') || lower.endsWith('.java')) {
    // Both use a C-like grammar — good enough for syntax colors. The clike
    // mode keyword/atom sets are word-keyed maps; passing strings would type-
    // error since 6.x.
    const isKotlin = lower.endsWith('.kt')
    return StreamLanguage.define(clike({
      name: isKotlin ? 'kotlin' : 'java',
      keywords: keywordSetForCLike(isKotlin ? 'kotlin' : 'java'),
      atoms: { true: true, false: true, null: true },
    }))
  }
  if (lower.endsWith('.lua')) return StreamLanguage.define(lua)
  if (lower.endsWith('.sh') || lower.endsWith('.bash')) return StreamLanguage.define(shell)
  if (lower.endsWith('dockerfile') || lower === 'dockerfile') return StreamLanguage.define(dockerFile)
  return null
}

function keywordSetForCLike(lang: 'kotlin' | 'java'): Record<string, true> {
  const kotlin = 'val var fun class object interface trait enum data sealed open override abstract internal private public protected return if else when while for do try catch finally throw in is as typeof package import constructor init companion this super by where suspend out vararg infix operator inline noinline crossinline reified delegate it'
  const java = 'class interface extends implements public private protected static final abstract void return if else while do switch case break continue new throw throws try catch finally synchronized volatile transient native package import this super instanceof'
  const words = lang === 'kotlin' ? kotlin : java
  return Object.fromEntries(words.split(/\s+/).map((w) => [w, true])) as Record<string, true>
}
