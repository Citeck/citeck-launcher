import { useParams } from 'react-router'
import { AppConfigEditor, type AppConfigEditorHandle } from '../components/AppConfigEditor'
import { useTranslation } from '../lib/i18n'
import { useEffect, useRef, useState } from 'react'
import { RotateCcw } from 'lucide-react'

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
  const editorRef = useRef<AppConfigEditorHandle>(null)
  const [dirty, setDirty] = useState(false)
  const [edited, setEdited] = useState(false)

  useEffect(() => {
    document.title = name ? `Config — ${name}` : 'Editor'
  }, [name])

  // Poll the editor handle once a render to keep the "edited" badge synced.
  // Cheap because the handle is created via useImperativeHandle and only
  // re-renders when the editor state changes.
  useEffect(() => {
    setEdited(editorRef.current?.isEdited() ?? false)
  })

  if (!name) {
    return (
      <div className="p-4 text-sm text-text-muted">
        {t('window.editor.noApp')}
      </div>
    )
  }

  function close() {
    // Best-effort close — works in the Wails secondary window. In a browser
    // tab fallback, window.close() is a no-op unless the tab was script-opened.
    window.close()
  }

  return (
    <div className="h-screen bg-background text-text flex flex-col">
      <header className="px-3 py-1.5 border-b border-border bg-bg-secondary text-sm flex items-center gap-2">
        <span className="text-text-muted">{t('window.editor.heading')}</span>
        <span className="font-medium">{name}</span>
      </header>
      <main className="flex-1 min-h-0 overflow-auto p-3">
        <AppConfigEditor
          ref={editorRef}
          appName={name}
          hideInnerActions
          onDirtyChange={setDirty}
        />
      </main>
      <footer className="border-t border-border bg-card px-3 py-2 flex items-center justify-end gap-2">
        {edited && (
          <button
            type="button"
            className="flex items-center gap-1 rounded border border-border px-3 py-1.5 text-xs text-muted-foreground hover:bg-muted"
            onClick={() => { void editorRef.current?.resetConfig() }}
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
          onClick={async () => { await editorRef.current?.apply() }}
        >
          {t('common.save')}
        </button>
      </footer>
    </div>
  )
}
