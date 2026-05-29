import { useParams, useSearchParams } from 'react-router'
import { AppConfigEditor, type AppConfigEditorHandle } from '../components/AppConfigEditor'
import { useTranslation } from '../lib/i18n'
import { useCallback, useEffect, useRef, useState } from 'react'
import { RotateCcw } from 'lucide-react'
import { useDashboardStore } from '../lib/store'
import { useInheritedTheme } from '../hooks/useInheritedTheme'
import { closeCurrentDesktopWindow } from '../lib/api'
import { WindowFileEditor } from './WindowFileEditor'
import { publishRefresh } from '../lib/windowBus'

/**
 * Standalone editor page used by native multi-window mode.
 * Renders the same {@link AppConfigEditor} that AppDetail uses, but in a
 * fullscreen layout without the app shell, with an explicit Reset / Cancel /
 * Submit action row at the bottom (Kotlin EditorWindow parity).
 *
 * Route: /window/editor/:name
 */
export function WindowEditor() {
  const { t } = useTranslation()
  const { name } = useParams<{ name: string }>()
  const [searchParams] = useSearchParams()
  // COG RMB menu → per-file edit dispatches to /window/editor/:name?file=...
  // The per-file UI is a distinct surface (no app YAML, no inner Lock/Reset
  // controls), so delegate to a dedicated component instead of cluttering
  // AppConfigEditor with an `initialFile` mode.
  const file = searchParams.get('file')
  if (file) {
    return <WindowFileEditor />
  }
  const editorRef = useRef<AppConfigEditorHandle>(null)
  const [dirty, setDirty] = useState(false)
  // Read "edited" (user-saved overrides on disk) from the dashboard store —
  // the same source AppConfigEditor uses — so no polling via the imperative
  // handle is needed and setState-in-effect is avoided.
  const edited = useDashboardStore((s) => s.namespace?.apps?.find((a) => a.name === name)?.edited ?? false)
  const fetchNamespaceData = useDashboardStore((s) => s.fetchData)
  // Wails secondary window has its own (empty) Zustand store + no SSE
  // subscription. Pull the namespace snapshot on mount so the Reset button
  // can correctly render based on the current `edited` flag, and refresh
  // after a successful save (publishRefresh is fire-and-forget; we want a
  // local refetch too).
  useEffect(() => { void fetchNamespaceData() }, [fetchNamespaceData])

  useInheritedTheme()

  useEffect(() => {
    document.title = name
      ? t('appConfig.tabTitle', { name })
      : t('window.editor.heading')
  }, [name, t])

  const close = useCallback(() => {
    if (!name) return
    void closeCurrentDesktopWindow({ kind: 'editor', id: name })
  }, [name])

  // Window-level keyboard shortcuts. Ctrl+F focuses the CodeMirror editor so
  // its built-in search panel opens — otherwise the user has to click into
  // the editor first and the shortcut feels broken. Escape closes the window
  // (except while typing in a control so a stray Esc doesn't drop edits).
  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      const ctrl = e.ctrlKey || e.metaKey
      if (ctrl && !e.shiftKey && !e.altKey && e.code === 'KeyF') {
        const cm = document.querySelector('.cm-content') as HTMLElement | null
        if (cm && document.activeElement !== cm) {
          e.preventDefault()
          cm.focus()
          // Re-emit Ctrl+F so CodeMirror's searchKeymap picks it up — without
          // this the user would have to press the shortcut twice.
          cm.dispatchEvent(new KeyboardEvent('keydown', {
            key: 'f', code: 'KeyF', ctrlKey: true, bubbles: true, cancelable: true,
          }))
        }
        return
      }
      if (e.key === 'Escape') {
        const tag = (document.activeElement as HTMLElement | null)?.tagName
        if (tag === 'INPUT' || tag === 'TEXTAREA') return
        // Don't close while CodeMirror's search panel has focus — let CM
        // handle Esc (close panel) first.
        if ((document.activeElement as HTMLElement | null)?.closest?.('.cm-panels')) return
        close()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [close])

  if (!name) {
    return (
      <div className="p-4 text-sm text-text-muted">
        {t('window.editor.noApp')}
      </div>
    )
  }

  return (
    <div className="h-screen bg-background text-foreground flex flex-col">
      <main className="flex-1 min-h-0 flex flex-col">
        <AppConfigEditor
          ref={editorRef}
          appName={name}
          hideInnerActions
          fullHeight
          onDirtyChange={setDirty}
        />
      </main>
      <footer className="border-t border-border bg-card px-3 py-2 flex items-center justify-end gap-2">
        {edited && (
          <button
            type="button"
            className="flex items-center gap-1 rounded border border-border px-3 py-1.5 text-xs text-foreground hover:bg-muted"
            onClick={async () => {
              await editorRef.current?.resetConfig()
              // After the on-disk override is cleared, ping the dashboard
              // so its table refetches the generator's def, then close —
              // mirrors the Save flow (Kotlin parity: Reset is a one-shot
              // commit that drops the window).
              publishRefresh()
              void fetchNamespaceData()
              close()
            }}
            title={t('appConfig.reset.tooltip')}
          >
            <RotateCcw size={12} />
            {t('appConfig.reset')}
          </button>
        )}
        <button
          type="button"
          className="rounded border border-border px-3 py-1.5 text-xs hover:bg-muted"
          onClick={() => { editorRef.current?.cancelEdit(); close() }}
        >
          {t('common.cancel')}
        </button>
        <button
          type="button"
          className="rounded bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          disabled={!dirty}
          onClick={async () => {
            const ok = await editorRef.current?.apply()
            if (ok) {
              // Refresh our own store so the Reset button shows up if the
              // app wasn't previously edited; ping the dashboard so its
              // table refetches; then close.
              void fetchNamespaceData()
              publishRefresh()
              close()
            }
          }}
        >
          {t('common.save')}
        </button>
      </footer>
    </div>
  )
}
