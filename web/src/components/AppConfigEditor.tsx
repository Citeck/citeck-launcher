import { useEffect, useState, useCallback, useImperativeHandle, forwardRef } from 'react'
import yaml from 'js-yaml'
import { getAppConfig, putAppConfig, resetAppConfig, putAppLock, getAppFiles, getAppFile, putAppFile, resetAppFile } from '../lib/api'
import { useDashboardStore } from '../lib/store'
import { ConfirmModal } from './ConfirmModal'
import { CodeEditor } from './CodeEditor'
import { YamlViewer } from './YamlViewer'
import { toast } from '../lib/toast'
import { useTranslation } from '../lib/i18n'
import { FileCode, Lock, Unlock, RotateCcw } from 'lucide-react'
import type { AppFileDto } from '../lib/types'
import { isEditableFile } from '../lib/files'

/**
 * Imperative handle for window-mode embedding (WindowEditor). Lets the parent
 * standalone window drive the editor's Apply / Cancel / Reset paths without
 * duplicating state.
 */
export interface AppConfigEditorHandle {
  /** True when the YAML buffer differs from the saved config. */
  isDirty(): boolean
  /** True when a non-default config exists on disk (lock + reset enabled). */
  isEdited(): boolean
  /** Save the current YAML buffer (no-op if not dirty). Returns promise that resolves on success/error. */
  apply(): Promise<void>
  /** Revert the in-memory edit buffer to the saved config. */
  cancelEdit(): void
  /** Reset the on-disk config back to the generator-supplied default. */
  resetConfig(): Promise<void>
}

interface AppConfigEditorProps {
  appName: string
  /**
   * When true, the per-app inner action buttons (Cancel/Apply/Reset/Lock) are
   * hidden. Used by WindowEditor which renders its own bottom action row and
   * drives this editor via the {@link AppConfigEditorHandle} ref.
   */
  hideInnerActions?: boolean
  /** Notify parent when the dirty state changes. */
  onDirtyChange?: (dirty: boolean) => void
}

export const AppConfigEditor = forwardRef<AppConfigEditorHandle, AppConfigEditorProps>(function AppConfigEditorImpl(
  { appName, hideInnerActions = false, onDirtyChange },
  ref,
) {
  const { t } = useTranslation()
  const [configYaml, setConfigYaml] = useState<string | null>(null)
  const [editYaml, setEditYaml] = useState<string | null>(null)
  const [configEditing, setConfigEditing] = useState(false)
  const [configSaving, setConfigSaving] = useState(false)
  const [configError, setConfigError] = useState<string | null>(null)
  const [showApplyConfirm, setShowApplyConfirm] = useState(false)
  const [showResetConfirm, setShowResetConfirm] = useState(false)
  const [resetting, setResetting] = useState(false)
  const [files, setFiles] = useState<AppFileDto[]>([])
  const [editingFile, setEditingFile] = useState<string | null>(null)
  const [fileContent, setFileContent] = useState('')
  const [fileSaving, setFileSaving] = useState(false)
  const [fileResetting, setFileResetting] = useState(false)
  const [fileError, setFileError] = useState<string | null>(null)
  const [loaded, setLoaded] = useState(false)

  const nsApps = useDashboardStore((s) => s.namespace?.apps)
  const appMeta = nsApps?.find((a) => a.name === appName)
  const isEdited = appMeta?.edited ?? false
  const isLocked = appMeta?.locked ?? false

  const load = useCallback(() => {
    const controller = new AbortController()
    const signal = controller.signal
    getAppConfig(appName)
      .then((y) => { if (!signal.aborted) { setConfigYaml(y); setEditYaml(y) } })
      .catch(() => { if (!signal.aborted) setConfigYaml(null) })
      .finally(() => { if (!signal.aborted) setLoaded(true) })
    getAppFiles(appName)
      .then((f) => { if (!signal.aborted) setFiles(f) })
      .catch(() => {})
    return controller
  }, [appName])

  useEffect(() => {
    const controller = load()
    return () => controller.abort()
  }, [load])

  // Window mode: always edit, so the outer Reset/Cancel/Submit row drives the
  // single mode of operation. We flip configEditing as soon as the YAML loads.
  useEffect(() => {
    if (hideInnerActions && configYaml !== null && !configEditing) {
      setConfigEditing(true)
    }
  }, [hideInnerActions, configYaml, configEditing])

  // Notify the optional parent (window mode) whenever dirty state flips so the
  // outer Apply / Submit button can enable/disable in sync.
  const isDirty = configEditing && editYaml !== configYaml
  useEffect(() => {
    onDirtyChange?.(isDirty)
  }, [isDirty, onDirtyChange])

  async function handleApplyConfig() {
    if (!editYaml) return
    // Real YAML validation (Kotlin EditorWindow parity) — js-yaml parse catches
    // indentation errors, dangling keys, broken anchors, etc. The daemon also
    // validates, but failing fast here gives a clearer client-side message.
    try {
      validateYamlContent(editYaml, 'app-config.yml')
    } catch (e) {
      setConfigError((e as Error).message)
      return
    }
    setConfigSaving(true); setConfigError(null)
    try {
      await putAppConfig(appName, editYaml)
      setConfigYaml(editYaml)
      setConfigEditing(false)
      setShowApplyConfirm(false)
      toast(t('appConfig.saved'), 'success')
    } catch (e) {
      setConfigError((e as Error).message)
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

  // Expose imperative methods for window-mode embedding (WindowEditor).
  useImperativeHandle(ref, () => ({
    isDirty: () => isDirty,
    isEdited: () => isEdited,
    apply: () => handleApplyConfig(),
    cancelEdit: () => {
      setEditYaml(configYaml)
      setConfigEditing(false)
      setConfigError(null)
    },
    resetConfig: () => handleResetConfig(),
  // handleApplyConfig / handleResetConfig close over current state; recompute
  // the handle on every render to avoid stale closures.
   
  }))

  if (!loaded) {
    return (
      <div className="p-2 space-y-2">
        {Array.from({ length: 3 }).map((_, i) => (
          <div key={i} className="h-3 w-full bg-muted rounded animate-pulse" />
        ))}
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
              {isEdited && <span className="text-[10px] text-blue-500 font-normal ml-1">{t('appConfig.edited', { detail: isLocked ? ', locked' : '' })}</span>}
            </div>
            {!hideInnerActions && (
              <div className="flex items-center gap-1">
                {isEdited && (
                  <>
                    <button type="button"
                      className={`flex items-center gap-0.5 rounded border border-border px-2 py-0.5 text-xs hover:bg-muted ${isLocked ? 'text-blue-500' : 'text-muted-foreground'}`}
                      title={isLocked ? t('appConfig.lock.unlockTooltip') : t('appConfig.lock.lockTooltip')}
                      onClick={() => putAppLock(appName, !isLocked).catch((e) => setConfigError((e as Error).message))}>
                      {isLocked ? <Lock size={11} /> : <Unlock size={11} />}
                      {isLocked ? t('appConfig.lock.locked') : t('appConfig.lock.unlocked')}
                    </button>
                    <button type="button"
                      className="flex items-center gap-0.5 rounded border border-border px-2 py-0.5 text-xs hover:bg-muted text-muted-foreground"
                      title={t('appConfig.reset.tooltip')}
                      onClick={() => setShowResetConfirm(true)}>
                      <RotateCcw size={11} /> {t('appConfig.reset')}
                    </button>
                  </>
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
            )}
          </div>
          {configError && <div className="text-[11px] text-destructive mb-1">{configError}</div>}
          {configEditing ? (
            <div className="rounded border border-border overflow-hidden">
              <CodeEditor
                value={editYaml ?? ''}
                onChange={setEditYaml}
                filename="app-config.yml"
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
          break the textual editor). */}
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
                    const content = await getAppFile(appName, f.path)
                    setEditingFile(f.path); setFileContent(content); setFileError(null)
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
                {fileError && <div className="text-[10px] text-destructive mb-1">{fileError}</div>}
                <div className="rounded border border-border overflow-hidden">
                  <CodeEditor
                    value={fileContent}
                    onChange={setFileContent}
                    filename={editingFile}
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
})

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
