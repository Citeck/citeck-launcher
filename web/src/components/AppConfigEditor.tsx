import { useEffect, useState, useCallback } from 'react'
import yaml from 'js-yaml'
import { getAppConfig, putAppConfig, resetAppConfig, getAppFiles, getAppFile, putAppFile, resetAppFile } from '../lib/api'
import { useDashboardStore } from '../lib/store'
import { ConfirmModal } from './ConfirmModal'
import { CodeEditor } from './CodeEditor'
import { YamlViewer } from './YamlViewer'
import { toast } from '../lib/toast'
import { useTranslation } from '../lib/i18n'
import { FileCode, RotateCcw } from 'lucide-react'
import type { AppFileDto } from '../lib/types'
import { isEditableFile } from '../lib/files'

interface AppConfigEditorProps {
  appName: string
}

export function AppConfigEditor({ appName }: AppConfigEditorProps) {
  const { t } = useTranslation()
  const [configYaml, setConfigYaml] = useState<string | null>(null)
  const [editYaml, setEditYaml] = useState<string | null>(null)
  // Generated baseline (no user patch) for the editor's change gutter.
  const [baseline, setBaselineYaml] = useState('')
  const [configEditing, setConfigEditing] = useState(false)
  const [configSaving, setConfigSaving] = useState(false)
  const [configError, setConfigError] = useState<string | null>(null)
  const [showApplyConfirm, setShowApplyConfirm] = useState(false)
  const [showResetConfirm, setShowResetConfirm] = useState(false)
  const [resetting, setResetting] = useState(false)
  const [files, setFiles] = useState<AppFileDto[]>([])
  const [editingFile, setEditingFile] = useState<string | null>(null)
  const [fileContent, setFileContent] = useState('')
  const [fileBaseline, setFileBaseline] = useState('')
  const [fileSaving, setFileSaving] = useState(false)
  const [fileResetting, setFileResetting] = useState(false)
  const [fileError, setFileError] = useState<string | null>(null)
  const [loaded, setLoaded] = useState(false)
  // True once getAppConfig has returned actual YAML. Stays false on load
  // errors so the save path stays locked — otherwise an editor opened with
  // an empty placeholder would overwrite the real config with "" on save.
  const [loadOK, setLoadOK] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)

  const nsApps = useDashboardStore((s) => s.namespace?.apps)
  const appMeta = nsApps?.find((a) => a.name === appName)
  const isEdited = appMeta?.edited ?? false
  // appMeta.locked still arrives in the namespace DTO for backwards-compat
  // with older daemons; the editor no longer surfaces an unlock control (any
  // saved edit is permanently authoritative until Reset, by design).

  const load = useCallback(() => {
    const controller = new AbortController()
    const signal = controller.signal
    setConfigError(null)
    setLoadError(null)
    setLoadOK(false)
    getAppConfig(appName)
      .then((dto) => {
        if (signal.aborted) return
        // Empty string is a legitimate result (app has no overrides yet) —
        // show the editor with empty content so the user can start typing.
        setConfigYaml(dto.content ?? '')
        setEditYaml(dto.content ?? '')
        setBaselineYaml(dto.baseline ?? '')
        setLoadOK(true)
      })
      .catch((e) => {
        if (signal.aborted) return
        console.error('[AppConfigEditor] getAppConfig failed:', e)
        const msg = (e as Error).message || String(e)
        setLoadError(msg)
        // Leave configYaml as null so the render branch knows the load
        // failed; the editor is shown in read-only mode with an error
        // banner and a Retry button so the user can recover without
        // closing and re-opening the window.
        setConfigYaml(null)
        setEditYaml(null)
      })
      .finally(() => { if (!signal.aborted) setLoaded(true) })
    getAppFiles(appName)
      .then((f) => { if (!signal.aborted) setFiles(f) })
      .catch(() => {})
    return controller
  }, [appName])

  useEffect(() => {
    // Intentional: load-on-mount / on-appName-change clears the loading flag
    // then fetches; not a cascading render. AbortController cleans up in-flight.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    const controller = load()
    return () => controller.abort()
  }, [load])

  async function handleApplyConfig(): Promise<boolean> {
    if (!loadOK) {
      setConfigError(t('appConfig.loadError.cannotSave'))
      return false
    }
    if (!editYaml) return false
    // Real YAML validation (Kotlin EditorWindow parity) — js-yaml parse catches
    // indentation errors, dangling keys, broken anchors, etc. The daemon also
    // validates, but failing fast here gives a clearer client-side message.
    try {
      validateYamlContent(editYaml, 'app-config.yml')
    } catch (e) {
      setConfigError((e as Error).message)
      return false
    }
    setConfigSaving(true); setConfigError(null)
    try {
      await putAppConfig(appName, editYaml)
      // Reload from the daemon so the editor reflects what was actually
      // stored (the daemon resets a few structural fields like image / ports
      // for safety, so the submitted YAML and the stored YAML can differ).
      try {
        const stored = await getAppConfig(appName)
        setConfigYaml(stored.content ?? '')
        setEditYaml(stored.content ?? '')
        setBaselineYaml(stored.baseline ?? '')
      } catch {
        // Daemon accepted the write but the post-save read failed — keep the
        // in-memory buffer the user submitted so they don't see stale data.
        setConfigYaml(editYaml)
      }
      setConfigEditing(false)
      setShowApplyConfirm(false)
      toast(t('appConfig.saved'), 'success')
      return true
    } catch (e) {
      setConfigError((e as Error).message)
      return false
    } finally {
      setConfigSaving(false)
    }
  }

  async function handleResetConfig() {
    setResetting(true); setConfigError(null)
    try {
      await resetAppConfig(appName)
      setShowResetConfirm(false)
      setConfigEditing(false)
      toast(t('appConfig.reset.success'), 'success')
      // Re-load to pick up the regenerated default.
      load()
    } catch (e) {
      setConfigError((e as Error).message)
    } finally {
      setResetting(false)
    }
  }

  if (!loaded) {
    return (
      <div className="p-2 space-y-2">
        {Array.from({ length: 3 }).map((_, i) => (
          <div key={i} className="h-3 w-full bg-muted rounded animate-pulse" />
        ))}
      </div>
    )
  }

  if (loadError) {
    return (
      <div className="p-3 space-y-3">
        <div className="rounded border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          <div className="font-medium mb-1">{t('appConfig.loadError.title')}</div>
          <div className="font-mono break-all">{loadError}</div>
        </div>
        <div className="text-[11px] text-muted-foreground">
          {t('appConfig.loadError.hint')}
        </div>
        <button
          type="button"
          className="rounded border border-border px-3 py-1.5 text-xs hover:bg-muted"
          onClick={() => load()}
        >
          {t('common.retry')}
        </button>
      </div>
    )
  }

  return (
    <div className="p-2 space-y-2 overflow-y-auto h-full">
      {/* App config editor */}
      {configYaml !== null ? (
        <div className="rounded border border-border bg-card p-2">
          <div className="flex items-center justify-between mb-1">
            <div className="flex items-center gap-1 text-xs font-medium">
              <FileCode size={13} /> {t('appConfig.title')}
              {isEdited && <span className="text-[10px] text-blue-500 font-normal ml-1">{t('appConfig.edited', { detail: '' })}</span>}
            </div>
            <div className="flex items-center gap-1">
              {isEdited && (
                // Reset is the ONLY way back to the generator's def — by
                // design, any saved edit is permanently authoritative until
                // Reset is hit. No Lock/Unlock toggle: edits are implicitly
                // locked by save, and surfacing the toggle would suggest
                // the launcher might silently re-apply the generator's
                // version on regenerate, which is not the model.
                <button type="button"
                  className="flex items-center gap-0.5 rounded border border-border px-2 py-0.5 text-xs hover:bg-muted text-muted-foreground"
                  title={t('appConfig.reset.tooltip')}
                  onClick={() => setShowResetConfirm(true)}>
                  <RotateCcw size={11} /> {t('appConfig.reset')}
                </button>
              )}
              {!configEditing ? (
                <button type="button" className="rounded border border-border px-2 py-0.5 text-xs hover:bg-muted"
                  onClick={() => { setEditYaml(configYaml); setConfigEditing(true); setConfigError(null) }}>
                  {t('common.edit')}
                </button>
              ) : (
                <div className="flex gap-1">
                  <button type="button" className="rounded border border-border px-2 py-0.5 text-xs hover:bg-muted"
                    onClick={() => setConfigEditing(false)}>{t('common.cancel')}</button>
                  <button type="button" className="rounded bg-primary px-2 py-0.5 text-xs text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
                    onClick={() => setShowApplyConfirm(true)} disabled={editYaml === configYaml}>{t('common.apply')}</button>
                </div>
              )}
            </div>
          </div>
          {configError && <div className="text-[11px] text-destructive mb-1 font-mono whitespace-pre overflow-auto max-h-40">{configError}</div>}
          {configEditing ? (
            <div className="rounded border border-border overflow-hidden">
              <CodeEditor
                value={editYaml ?? ''}
                onChange={setEditYaml}
                filename="app-config.yml"
                baseline={baseline}
                height="350px"
                autoFocus
              />
            </div>
          ) : (
            <div className="max-h-48 overflow-auto">
              <YamlViewer content={configYaml} />
            </div>
          )}
        </div>
      ) : (
        <div className="rounded border border-border bg-card p-2 text-xs text-muted-foreground">
          {t('appConfig.noConfig')}
        </div>
      )}

      {/* Mounted Files — only editable extensions, matching the COG RMB menu
          and Kotlin v1.3.8 behaviour (binaries like fonts/jars/certs would
          break the textual editor). In window mode the COG RMB menu owns
          per-file edits via dedicated WindowEditor windows, so this panel
          only renders inside the main shell (AppDetail / drawer). */}
      {files.filter((f) => isEditableFile(f.path)).length > 0 && (
        <div className="rounded border border-border bg-card p-2">
          <div className="text-xs font-medium mb-1">{t('appConfig.files')}</div>
          {files.filter((f) => isEditableFile(f.path)).map((f) => (
            <div key={f.path} className="flex items-center gap-2 text-[11px] font-mono">
              {f.edited && <span className="inline-block w-0.5 h-3 bg-blue-500 mr-1.5 align-middle shrink-0" title={t('appConfig.fileEdited.badge')} />}
              <span className="text-muted-foreground flex-1 break-all">{f.path}</span>
              <button type="button" className="text-primary hover:underline text-[10px] shrink-0"
                onClick={async () => {
                  try {
                    const dto = await getAppFile(appName, f.path)
                    setEditingFile(f.path); setFileContent(dto.content); setFileBaseline(dto.baseline ?? ''); setFileError(null)
                  } catch (e) { setFileError((e as Error).message) }
                }}>{t('common.edit')}</button>
            </div>
          ))}
          {fileError && !editingFile && <div className="text-[10px] text-destructive mt-1">{fileError}</div>}
          {editingFile && (() => {
            const editingMeta = files.find((f) => f.path === editingFile)
            const isFileEdited = editingMeta?.edited ?? false
            return (
              <div className="mt-2 border-t border-border pt-2">
                <div className="flex items-center justify-between mb-1">
                  <span className="text-[11px] font-mono text-muted-foreground">{editingFile}</span>
                  <div className="flex gap-1">
                    {isFileEdited && (
                      <button type="button"
                        className="flex items-center gap-0.5 rounded border border-border px-2 py-0.5 text-xs hover:bg-muted text-muted-foreground disabled:opacity-50"
                        title={t('appConfig.fileReset.tooltip')}
                        disabled={fileResetting || fileSaving}
                        onClick={async () => {
                          if (!editingFile) return
                          setFileResetting(true); setFileError(null)
                          try {
                            await resetAppFile(appName, editingFile)
                            setEditingFile(null)
                            toast(t('appConfig.fileReset.success'), 'success')
                            load()
                          } catch (e) {
                            setFileError((e as Error).message)
                          } finally {
                            setFileResetting(false)
                          }
                        }}>
                        <RotateCcw size={11} /> {t('appConfig.reset')}
                      </button>
                    )}
                    <button type="button" className="rounded border border-border px-2 py-0.5 text-xs hover:bg-muted"
                      onClick={() => { setEditingFile(null); setFileError(null) }}>{t('common.cancel')}</button>
                    <button type="button" className="rounded bg-primary px-2 py-0.5 text-xs text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
                      disabled={fileSaving || fileResetting}
                      onClick={async () => {
                        setFileSaving(true); setFileError(null)
                        try {
                          // Validate YAML / JSON before round-trip when the
                          // mounted file extension implies a structured format.
                          if (editingFile.match(/\.(ya?ml|json)$/i)) {
                            try {
                              validateYamlContent(fileContent, editingFile)
                            } catch (e) {
                              setFileError((e as Error).message)
                              setFileSaving(false)
                              return
                            }
                          }
                          await putAppFile(appName, editingFile, fileContent)
                          setEditingFile(null)
                          toast(t('appConfig.fileSaved'), 'success')
                          load()
                        } catch (e) { setFileError((e as Error).message) }
                        finally { setFileSaving(false) }
                      }}>{fileSaving ? t('common.saving') : t('common.save')}</button>
                  </div>
                </div>
                {fileError && <div className="text-[10px] text-destructive mb-1 font-mono whitespace-pre overflow-auto max-h-40">{fileError}</div>}
                <div className="rounded border border-border overflow-hidden">
                  <CodeEditor
                    value={fileContent}
                    onChange={setFileContent}
                    filename={editingFile}
                    baseline={fileBaseline}
                    height="300px"
                    autoFocus
                  />
                </div>
              </div>
            )
          })()}
        </div>
      )}

      <ConfirmModal open={showApplyConfirm} title={t('appConfig.confirm.title')}
        message={t('appConfig.confirm.message')}
        confirmLabel={t('common.apply')} loading={configSaving}
        onConfirm={handleApplyConfig} onCancel={() => setShowApplyConfirm(false)} />
      <ConfirmModal open={showResetConfirm} title={t('appConfig.reset.confirmTitle')}
        message={t('appConfig.reset.confirmMessage')}
        confirmLabel={t('appConfig.reset')} confirmVariant="danger" loading={resetting}
        onConfirm={handleResetConfig} onCancel={() => setShowResetConfirm(false)} />
    </div>
  )
}

/**
 * Real YAML / JSON validation (Kotlin EditorWindow.validate parity).
 *
 * The Kotlin window used Jackson's Yaml.read / Json.read to fail fast on
 * malformed input before the runtime touched it; we mirror that with
 * js-yaml's parser. JSON-ending filenames are validated by JSON.parse as
 * well — js-yaml accepts JSON since it's a superset of YAML, but the JSON
 * parse gives clearer errors when the file is intentionally JSON.
 */
function validateYamlContent(content: string, filename: string): void {
  if (content.trim() === '') {
    throw new Error('YAML is empty')
  }
  try {
    yaml.load(content)
  } catch (e) {
    throw new Error('Invalid YAML: ' + (e as Error).message, { cause: e })
  }
  if (filename.toLowerCase().endsWith('.json')) {
    try {
      JSON.parse(content)
    } catch (e) {
      throw new Error('Invalid JSON: ' + (e as Error).message, { cause: e })
    }
  }
}
