import { useEffect, useState, useCallback } from 'react'
import { getAppConfig, putAppConfig, putAppLock, getAppFiles, getAppFile, putAppFile } from '../lib/api'
import { useDashboardStore } from '../lib/store'
import { ConfirmModal } from './ConfirmModal'
import { YamlViewer } from './YamlViewer'
import { toast } from '../lib/toast'
import { useTranslation } from '../lib/i18n'
import { FileCode, Lock, Unlock } from 'lucide-react'

interface AppConfigEditorProps {
  appName: string
}

export function AppConfigEditor({ appName }: AppConfigEditorProps) {
  const { t } = useTranslation()
  const [configYaml, setConfigYaml] = useState<string | null>(null)
  const [editYaml, setEditYaml] = useState<string | null>(null)
  const [configEditing, setConfigEditing] = useState(false)
  const [configSaving, setConfigSaving] = useState(false)
  const [configError, setConfigError] = useState<string | null>(null)
  const [showApplyConfirm, setShowApplyConfirm] = useState(false)
  const [files, setFiles] = useState<string[]>([])
  const [editingFile, setEditingFile] = useState<string | null>(null)
  const [fileContent, setFileContent] = useState('')
  const [fileSaving, setFileSaving] = useState(false)
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

  async function handleApplyConfig() {
    if (!editYaml) return
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
            <div className="flex items-center gap-1">
              {isEdited && (
                <button type="button"
                  className={`flex items-center gap-0.5 rounded border border-border px-2 py-0.5 text-xs hover:bg-muted ${isLocked ? 'text-blue-500' : 'text-muted-foreground'}`}
                  title={isLocked ? t('appConfig.lock.unlockTooltip') : t('appConfig.lock.lockTooltip')}
                  onClick={() => putAppLock(appName, !isLocked).catch((e) => setConfigError((e as Error).message))}>
                  {isLocked ? <Lock size={11} /> : <Unlock size={11} />}
                  {isLocked ? t('appConfig.lock.locked') : t('appConfig.lock.unlocked')}
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
          {configError && <div className="text-[11px] text-destructive mb-1">{configError}</div>}
          {configEditing ? (
            <textarea className="w-full rounded border border-border bg-background p-2 font-mono text-[11px] text-foreground focus:border-primary focus:outline-none"
              rows={Math.max(10, (editYaml ?? '').split('\n').length + 1)}
              value={editYaml ?? ''} onChange={(e) => setEditYaml(e.target.value)} spellCheck={false} />
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

      {/* Mounted Files */}
      {files.length > 0 && (
        <div className="rounded border border-border bg-card p-2">
          <div className="text-xs font-medium mb-1">{t('appConfig.files')}</div>
          {files.map((f) => (
            <div key={f} className="flex items-center gap-2 text-[11px] font-mono">
              <span className="text-muted-foreground flex-1 break-all">{f}</span>
              <button type="button" className="text-primary hover:underline text-[10px] shrink-0"
                onClick={async () => {
                  try {
                    const content = await getAppFile(appName, f)
                    setEditingFile(f); setFileContent(content); setFileError(null)
                  } catch (e) { setFileError((e as Error).message) }
                }}>{t('common.edit')}</button>
            </div>
          ))}
          {fileError && !editingFile && <div className="text-[10px] text-destructive mt-1">{fileError}</div>}
          {editingFile && (
            <div className="mt-2 border-t border-border pt-2">
              <div className="flex items-center justify-between mb-1">
                <span className="text-[11px] font-mono text-muted-foreground">{editingFile}</span>
                <div className="flex gap-1">
                  <button type="button" className="rounded border border-border px-2 py-0.5 text-xs hover:bg-muted"
                    onClick={() => { setEditingFile(null); setFileError(null) }}>{t('common.cancel')}</button>
                  <button type="button" className="rounded bg-primary px-2 py-0.5 text-xs text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
                    disabled={fileSaving}
                    onClick={async () => {
                      setFileSaving(true); setFileError(null)
                      try {
                        await putAppFile(appName, editingFile, fileContent)
                        setEditingFile(null)
                        toast(t('appConfig.fileSaved'), 'success')
                      } catch (e) { setFileError((e as Error).message) }
                      finally { setFileSaving(false) }
                    }}>{fileSaving ? t('common.saving') : t('common.save')}</button>
                </div>
              </div>
              {fileError && <div className="text-[10px] text-destructive mb-1">{fileError}</div>}
              <textarea className="w-full rounded border border-border bg-background p-2 font-mono text-[11px] text-foreground focus:border-primary focus:outline-none"
                rows={Math.max(8, fileContent.split('\n').length + 1)}
                value={fileContent} onChange={(e) => setFileContent(e.target.value)} spellCheck={false} />
            </div>
          )}
        </div>
      )}

      <ConfirmModal open={showApplyConfirm} title={t('appConfig.confirm.title')}
        message={t('appConfig.confirm.message')}
        confirmLabel={t('common.apply')} loading={configSaving}
        onConfirm={handleApplyConfig} onCancel={() => setShowApplyConfirm(false)} />
    </div>
  )
}
