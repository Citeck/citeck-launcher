import { useParams, useSearchParams } from 'react-router'
import { useCallback, useEffect, useRef, useState } from 'react'
import yaml from 'js-yaml'
import { getAppFile, putAppFile, resetAppFile, closeCurrentDesktopWindow } from '../lib/api'
import { CodeEditor } from '../components/CodeEditor'
import { useTranslation } from '../lib/i18n'
import { useInheritedTheme } from '../hooks/useInheritedTheme'
import { toast } from '../lib/toast'
import { RotateCcw } from 'lucide-react'
import { useDashboardStore } from '../lib/store'

/**
 * Per-file editor used by the COG RMB menu's "open this mounted file"
 * action in desktop mode. Routes through /window/editor/:name?file=<path>
 * so {@link WindowEditor} can dispatch to this component when a `file`
 * query param is present. Keeps the YAML config editor (no `file`) and
 * the per-file editor as two distinct windows the user can have open
 * simultaneously, mirroring Kotlin 1.x.
 */
export function WindowFileEditor() {
  const { t } = useTranslation()
  const { name } = useParams<{ name: string }>()
  const [params] = useSearchParams()
  const filePath = params.get('file') ?? ''
  const [content, setContent] = useState<string | null>(null)
  const [editContent, setEditContent] = useState('')
  const [loaded, setLoaded] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [resetting, setResetting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const loadRef = useRef<() => void>(undefined)

  useInheritedTheme()

  const apps = useDashboardStore((s) => s.namespace?.apps)
  // The cog menu surfaces an "edited" badge sourced from `/apps/<name>/files`
  // — the per-file editor mirrors it so the Reset button only shows when
  // the file actually has a user override on disk.
  const editedFiles = useDashboardStore((s) => s.namespace?.apps?.find((a) => a.name === name)?.editedFilesCount ?? 0)
  // Without an `apps` snapshot we can't know if the path is even part of
  // the live runtime; guard against null so the editor doesn't pretend
  // everything's fine on a fresh load.
  const appExists = !!apps?.find((a) => a.name === name)

  useEffect(() => {
    document.title = filePath ? `${name} — ${filePath}` : (name ?? t('window.editor.heading'))
  }, [name, filePath, t])

  const close = useCallback(() => {
    if (!name) return
    void closeCurrentDesktopWindow({ kind: 'editor', id: `${name}::${filePath}` })
  }, [name, filePath])

  const load = useCallback(() => {
    if (!name || !filePath) return
    setLoadError(null)
    getAppFile(name, filePath)
      .then((c) => {
        setContent(c)
        setEditContent(c)
      })
      .catch((e) => {
        setLoadError((e as Error).message || String(e))
        setContent(null)
        setEditContent('')
      })
      .finally(() => setLoaded(true))
  }, [name, filePath])
  loadRef.current = load

  useEffect(() => { load() }, [load])

  // Window-level shortcuts: Ctrl+F focuses CodeMirror (its searchKeymap
  // opens the search panel), Escape closes — but not while CM's search
  // panel has focus (so the first Esc closes the panel).
  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      const ctrl = e.ctrlKey || e.metaKey
      if (ctrl && !e.shiftKey && !e.altKey && e.code === 'KeyF') {
        const cm = document.querySelector('.cm-content') as HTMLElement | null
        if (cm && document.activeElement !== cm) {
          e.preventDefault()
          cm.focus()
          cm.dispatchEvent(new KeyboardEvent('keydown', {
            key: 'f', code: 'KeyF', ctrlKey: true, bubbles: true, cancelable: true,
          }))
        }
        return
      }
      if (e.key === 'Escape') {
        const tag = (document.activeElement as HTMLElement | null)?.tagName
        if (tag === 'INPUT' || tag === 'TEXTAREA') return
        if ((document.activeElement as HTMLElement | null)?.closest?.('.cm-panels')) return
        close()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [close])

  if (!name || !filePath) {
    return (
      <div className="p-4 text-sm text-muted-foreground">
        {t('window.editor.noApp')}
      </div>
    )
  }

  if (!loaded) {
    return (
      <div className="p-4 space-y-2">
        {Array.from({ length: 3 }).map((_, i) => (
          <div key={i} className="h-3 w-full bg-muted rounded animate-pulse" />
        ))}
      </div>
    )
  }

  const loadOK = !loadError && content !== null
  const dirty = loadOK && editContent !== content

  async function handleSave(): Promise<boolean> {
    if (!loadOK) {
      setError(t('appConfig.loadError.cannotSave'))
      return false
    }
    // Validate structured formats (YAML / JSON) before round-trip — mirrors
    // the validation that the inline file editor does inside AppConfigEditor.
    if (/\.(ya?ml|json)$/i.test(filePath)) {
      try {
        if (editContent.trim() === '') throw new Error('YAML is empty')
        yaml.load(editContent)
        if (filePath.toLowerCase().endsWith('.json')) JSON.parse(editContent)
      } catch (e) {
        setError(`Invalid ${filePath.toLowerCase().endsWith('.json') ? 'JSON' : 'YAML'}: ${(e as Error).message}`)
        return false
      }
    }
    setSaving(true); setError(null)
    try {
      if (!name) return false
      await putAppFile(name, filePath, editContent)
      // Re-fetch to see what daemon actually persisted (mirrors the
      // app-config editor's post-save reload).
      try {
        const stored = await getAppFile(name, filePath)
        setContent(stored)
        setEditContent(stored)
      } catch { setContent(editContent) }
      toast(t('appConfig.fileSaved'), 'success')
      return true
    } catch (e) {
      setError((e as Error).message)
      return false
    } finally {
      setSaving(false)
    }
  }

  async function handleReset() {
    if (!name) return
    setResetting(true); setError(null)
    try {
      await resetAppFile(name, filePath)
      toast(t('appConfig.fileReset.success'), 'success')
      loadRef.current?.()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setResetting(false)
    }
  }

  return (
    <div className="h-screen bg-background text-foreground flex flex-col">
      <main className="flex-1 min-h-0 flex flex-col">
        {loadError ? (
          <div className="p-3 space-y-3">
            <div className="rounded border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
              <div className="font-medium mb-1">{t('appConfig.loadError.title')}</div>
              <div className="font-mono break-all">{loadError}</div>
            </div>
            <button
              type="button"
              className="rounded border border-border px-3 py-1.5 text-xs hover:bg-muted"
              onClick={load}
            >
              {t('common.retry')}
            </button>
          </div>
        ) : (
          <>
            {error && (
              <div className="px-3 py-1.5 text-[11px] text-destructive border-b border-border shrink-0">{error}</div>
            )}
            <div className="flex-1 min-h-0 overflow-hidden">
              <CodeEditor
                value={editContent}
                onChange={setEditContent}
                filename={filePath}
                height="100%"
                autoFocus
              />
            </div>
          </>
        )}
      </main>
      <footer className="border-t border-border bg-card px-3 py-2 flex items-center justify-end gap-2">
        {editedFiles > 0 && appExists && (
          <button
            type="button"
            className="flex items-center gap-1 rounded border border-border px-3 py-1.5 text-xs text-muted-foreground hover:bg-muted disabled:opacity-50"
            onClick={handleReset}
            disabled={resetting || saving}
            title={t('appConfig.fileReset.tooltip')}
          >
            <RotateCcw size={12} />
            {t('appConfig.reset')}
          </button>
        )}
        <button
          type="button"
          className="rounded border border-border px-3 py-1.5 text-xs hover:bg-muted"
          onClick={close}
        >
          {t('common.cancel')}
        </button>
        <button
          type="button"
          className="rounded bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          disabled={!dirty || saving}
          onClick={async () => {
            const ok = await handleSave()
            if (ok) close()
          }}
        >
          {saving ? t('common.saving') : t('common.save')}
        </button>
      </footer>
    </div>
  )
}
