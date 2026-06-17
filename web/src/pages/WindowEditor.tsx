import { useParams, useSearchParams } from 'react-router'
import { useCallback, useEffect, useState } from 'react'
import yaml from 'js-yaml'
import { RotateCcw } from 'lucide-react'
import { CodeEditor } from '../components/CodeEditor'
import { LoadingLabel } from '../components/LoadingLabel'
import { useTranslation } from '../lib/i18n'
import {
  getAppConfig, putAppConfig, resetAppConfig,
  getAppFile, putAppFile, resetAppFile, getAppFiles,
  closeCurrentDesktopWindow,
} from '../lib/api'
import { useDashboardStore } from '../lib/store'
import { useInheritedTheme } from '../hooks/useInheritedTheme'
import { toast } from '../lib/toast'
import { publishRefresh } from '../lib/windowBus'

/**
 * Standalone editor used by the multi-window desktop mode. Drives two
 * surfaces under one component so the chrome, keyboard handlers, refresh
 * plumbing and footer button policy stay in sync:
 *
 *   /window/editor/:name              — app YAML config editor
 *   /window/editor/:name?file=<path>  — per-file editor (mounted bind-mount file)
 *
 * Both surfaces:
 *  - Load via REST, fail to a Retry screen on error (no half-loaded editor)
 *  - Save → re-fetch from daemon → publishRefresh() → close window
 *  - Reset → daemon-side reset → publishRefresh() → close window
 *  - Ctrl+F focuses CodeMirror; Esc closes the window (unless typing or
 *    CodeMirror's search panel has focus)
 */
export function WindowEditor() {
  const { t } = useTranslation()
  const { name } = useParams<{ name: string }>()
  const [params] = useSearchParams()
  const filePath = params.get('file')
  const isFile = !!filePath

  useInheritedTheme()

  const fetchNamespaceData = useDashboardStore((s) => s.fetchData)
  // Wails secondary windows start with an empty store; pull the namespace
  // snapshot so the Reset visibility selectors below resolve correctly.
  useEffect(() => { void fetchNamespaceData() }, [fetchNamespaceData])

  // For app-mode, the dashboard DTO's `edited` flag drives Reset visibility.
  // For file-mode, we ask /apps/<name>/files for this specific path.
  const appEdited = useDashboardStore((s) =>
    !isFile && name ? (s.namespace?.apps?.find((a) => a.name === name)?.edited ?? false) : false,
  )
  const [fileEdited, setFileEdited] = useState(false)

  const [content, setContent] = useState<string | null>(null)
  const [editContent, setEditContent] = useState('')
  // Generated baseline (no user patch) for the editor's change gutter.
  const [baseline, setBaseline] = useState('')
  const [loaded, setLoaded] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [resetting, setResetting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const close = useCallback(() => {
    if (!name) return
    const id = isFile ? `${name}::${filePath}` : name
    void closeCurrentDesktopWindow({ kind: 'editor', id })
  }, [name, filePath, isFile])

  useEffect(() => {
    if (!name) {
      document.title = t('window.editor.heading')
      return
    }
    if (isFile && filePath) {
      const slash = filePath.lastIndexOf('/')
      const basename = slash >= 0 ? filePath.slice(slash + 1) : filePath
      document.title = `${t('appConfig.tabTitle', { name })} — ${basename}`
    } else {
      document.title = t('appConfig.tabTitle', { name })
    }
  }, [name, filePath, isFile, t])

  const load = useCallback(async () => {
    if (!name) return
    setLoaded(false)
    setLoadError(null)
    setError(null)
    try {
      let text: string
      let base = ''
      if (isFile && filePath) {
        const dto = await getAppFile(name, filePath)
        text = dto.content
        base = dto.baseline ?? ''
        // edited-flag is a per-file fact maintained by the daemon's
        // /apps/<name>/files endpoint — read it back so Reset visibility
        // matches the COG RMB menu's badge.
        try {
          const files = await getAppFiles(name)
          const entry = files.find((f) => f.path === filePath)
          setFileEdited(entry?.edited ?? false)
        } catch { /* edited flag is non-essential; default false on error */ }
      } else {
        const dto = await getAppConfig(name)
        text = dto.content
        base = dto.baseline ?? ''
      }
      setContent(text ?? '')
      setEditContent(text ?? '')
      setBaseline(base)
    } catch (e) {
      setLoadError((e as Error).message || String(e))
      setContent(null)
      setEditContent('')
    } finally {
      setLoaded(true)
    }
  }, [name, filePath, isFile])

  // Intentional: one-shot loading flag for the on-mount file/config fetch
  // (reloads when name/filePath change); not a cascading render.
  // eslint-disable-next-line react-hooks/set-state-in-effect
  useEffect(() => { void load() }, [load])

  // Window-level shortcuts: Ctrl+F focuses CodeMirror (its built-in search
  // panel opens), Esc closes the window (unless typing or the CodeMirror
  // search panel currently has focus — first Esc closes the panel).
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

  if (!name) {
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

  if (loadError) {
    return (
      <div className="h-screen bg-background text-foreground flex flex-col p-3 space-y-3">
        <div className="rounded border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          <div className="font-medium mb-1">{t('appConfig.loadError.title')}</div>
          <div className="font-mono break-all">{loadError}</div>
        </div>
        <div className="text-[11px] text-muted-foreground">
          {t('appConfig.loadError.hint')}
        </div>
        <div>
          <button
            type="button"
            className="rounded border border-border px-3 py-1.5 text-xs hover:bg-muted"
            onClick={() => { void load() }}
          >
            {t('common.retry')}
          </button>
        </div>
      </div>
    )
  }

  const loadOK = content !== null
  const dirty = loadOK && editContent !== content
  const reseable = isFile ? fileEdited : appEdited
  // Filename hint drives CodeMirror's syntax highlighter; for app-YAML we
  // pretend it's a yml so YAML mode kicks in.
  const editorFilename = isFile && filePath ? filePath : 'app-config.yml'

  async function handleSave(): Promise<boolean> {
    if (!loadOK || !name) {
      setError(t('appConfig.loadError.cannotSave'))
      return false
    }
    // Validate structured formats before round-trip (mirrors the original
    // AppCfgEditWindow's pre-flight check in Kotlin).
    const lowerName = editorFilename.toLowerCase()
    if (/\.(ya?ml|json)$/i.test(lowerName)) {
      try {
        if (editContent.trim() === '') throw new Error('YAML is empty')
        yaml.load(editContent)
        if (lowerName.endsWith('.json')) JSON.parse(editContent)
      } catch (e) {
        setError(`Invalid ${lowerName.endsWith('.json') ? 'JSON' : 'YAML'}: ${(e as Error).message}`)
        return false
      }
    }
    setSaving(true); setError(null)
    try {
      if (isFile && filePath) {
        await putAppFile(name, filePath, editContent)
      } else {
        await putAppConfig(name, editContent)
      }
      // Re-fetch so the local buffer reflects whatever the daemon actually
      // stored (defense-in-depth resets, normalisation, etc).
      try {
        const stored = isFile && filePath
          ? await getAppFile(name, filePath)
          : await getAppConfig(name)
        setContent(stored.content ?? '')
        setEditContent(stored.content ?? '')
        setBaseline(stored.baseline ?? '')
      } catch {
        setContent(editContent)
      }
      toast(isFile ? t('appConfig.fileSaved') : t('appConfig.saved'), 'success')
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
      if (isFile && filePath) {
        await resetAppFile(name, filePath)
      } else {
        await resetAppConfig(name)
      }
      toast(isFile ? t('appConfig.fileReset.success') : t('appConfig.reset.success'), 'success')
    } catch (e) {
      setError((e as Error).message)
      throw e
    } finally {
      setResetting(false)
    }
  }

  return (
    <div className="h-screen bg-background text-foreground flex flex-col">
      <main className="flex-1 min-h-0 flex flex-col">
        {error && (
          // font-mono + whitespace-pre keeps js-yaml's multi-line snippet (line
          // gutter + the "^" caret under the offending column) aligned instead
          // of collapsing it into one run-on line; scroll if it's tall/wide.
          <div className="px-3 py-1.5 text-[11px] text-destructive border-b border-border shrink-0 font-mono whitespace-pre overflow-auto max-h-40">{error}</div>
        )}
        <div className="flex-1 min-h-0 overflow-hidden">
          <CodeEditor
            value={editContent}
            onChange={setEditContent}
            filename={editorFilename}
            baseline={baseline}
            height="100%"
            autoFocus
          />
        </div>
      </main>
      <footer className="border-t border-border bg-card px-3 py-2 flex items-center justify-end gap-2">
        {reseable && (
          <button
            type="button"
            className="flex items-center gap-1 rounded border border-border px-3 py-1.5 text-xs text-foreground hover:bg-muted disabled:opacity-50"
            disabled={resetting || saving}
            onClick={async () => {
              try { await handleReset() } catch { return }
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
            if (ok) {
              publishRefresh()
              void fetchNamespaceData()
              close()
            }
          }}
        >
          <LoadingLabel loading={saving}>{t('common.save')}</LoadingLabel>
        </button>
      </footer>
    </div>
  )
}
