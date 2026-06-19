import { useCallback, useEffect, useState } from 'react'
import yaml from 'js-yaml'
import { Loader2, RotateCcw } from 'lucide-react'
import { Modal } from './Modal'
import { CodeEditor } from './CodeEditor'
import { ConfirmModal } from './ConfirmModal'
import { getWorkspaceConfig, putWorkspaceConfig, resetWorkspaceConfig } from '../lib/api'
import { toast } from '../lib/toast'
import { useTranslation } from '../lib/i18n'

interface WorkspaceConfigDialogProps {
  workspaceId: string
  onClose: () => void
  /** Called after a successful save / reset so the parent can refetch Welcome data. */
  onSaved: () => void
}

const WS_CONFIG_FILE = 'workspace-v1.yml'

/**
 * Raw-YAML editor for the workspace configuration (workspace-v1.yml). The git
 * repo is the reference; the user's edits are stored as a delta on the daemon
 * and re-applied on top of the git reference at every resolve — the
 * workspace-scoped counterpart of the per-app AppConfigEditor (same CodeEditor +
 * change-gutter baseline).
 */
export function WorkspaceConfigDialog({ workspaceId, onClose, onSaved }: WorkspaceConfigDialogProps) {
  const { t } = useTranslation()
  const [content, setContent] = useState('')
  const [baseline, setBaseline] = useState('')
  const [loaded, setLoaded] = useState(false)
  const [loadOK, setLoadOK] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [resetting, setResetting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [showResetConfirm, setShowResetConfirm] = useState(false)

  const load = useCallback(() => {
    const controller = new AbortController()
    const signal = controller.signal
    setError(null)
    setLoadError(null)
    setLoadOK(false)
    getWorkspaceConfig(workspaceId)
      .then((dto) => {
        if (signal.aborted) return
        setContent(dto.content ?? '')
        setBaseline(dto.baseline ?? '')
        setLoadOK(true)
      })
      .catch((e) => {
        if (signal.aborted) return
        setLoadError((e as Error).message || String(e))
      })
      .finally(() => { if (!signal.aborted) setLoaded(true) })
    return controller
  }, [workspaceId])

  useEffect(() => {
    // Intentional: load-on-mount / on-workspace-change; AbortController cleans up.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    const controller = load()
    return () => controller.abort()
  }, [load])

  async function handleSave() {
    if (!loadOK) {
      setError(t('appConfig.loadError.cannotSave'))
      return
    }
    try {
      if (content.trim() === '') throw new Error('YAML is empty')
      yaml.load(content)
    } catch (e) {
      setError(t('workspace.config.invalidYaml', { error: (e as Error).message }))
      return
    }
    setSaving(true)
    setError(null)
    try {
      await putWorkspaceConfig(workspaceId, content)
      toast(t('workspace.config.saved'), 'success')
      onSaved()
      onClose()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  async function handleReset() {
    setResetting(true)
    setError(null)
    try {
      await resetWorkspaceConfig(workspaceId)
      setShowResetConfirm(false)
      toast(t('workspace.config.resetSuccess'), 'success')
      onSaved()
      onClose()
    } catch (e) {
      setError((e as Error).message)
      setShowResetConfirm(false)
    } finally {
      setResetting(false)
    }
  }

  const busy = saving || resetting
  const dirty = content !== baseline

  return (
    <>
      <Modal
        open
        width="lg"
        title={t('workspace.config.title')}
        onClose={onClose}
        footer={
          <>
            <button
              type="button"
              className="flex items-center gap-1 rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted text-muted-foreground disabled:opacity-50"
              onClick={() => setShowResetConfirm(true)}
              disabled={busy || !loadOK}
              title={t('workspace.config.resetTooltip')}
            >
              <RotateCcw size={12} /> {t('workspace.config.reset')}
            </button>
            <div className="flex gap-2">
              <button
                type="button"
                className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50"
                onClick={onClose}
                disabled={busy}
              >
                {t('common.cancel')}
              </button>
              <button
                type="button"
                className="rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-xs font-medium hover:bg-primary/90 disabled:opacity-50"
                onClick={handleSave}
                disabled={busy || !loadOK || !dirty}
              >
                {saving ? <Loader2 size={12} className="animate-spin" /> : t('common.save')}
              </button>
            </div>
          </>
        }
      >
        {!loaded ? (
          <div className="space-y-2 py-4">
            {Array.from({ length: 4 }).map((_, i) => (
              <div key={i} className="h-3 w-full bg-muted rounded animate-pulse" />
            ))}
          </div>
        ) : loadError ? (
          <div className="space-y-3">
            <div className="rounded border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
              <div className="font-medium mb-1">{t('appConfig.loadError.title')}</div>
              <div className="font-mono break-all">{loadError}</div>
            </div>
            <button
              type="button"
              className="rounded border border-border px-3 py-1.5 text-xs hover:bg-muted"
              onClick={() => load()}
            >
              {t('common.retry')}
            </button>
          </div>
        ) : (
          <>
            <p className="text-[11px] text-muted-foreground">{t('workspace.config.hint')}</p>
            {error && (
              <div className="text-[11px] text-destructive font-mono whitespace-pre overflow-auto max-h-40">{error}</div>
            )}
            <div className="rounded border border-border overflow-hidden">
              <CodeEditor
                value={content}
                onChange={setContent}
                filename={WS_CONFIG_FILE}
                baseline={baseline}
                height="420px"
                autoFocus
              />
            </div>
          </>
        )}
      </Modal>

      <ConfirmModal
        open={showResetConfirm}
        title={t('workspace.config.resetConfirmTitle')}
        message={t('workspace.config.resetConfirmMessage')}
        confirmLabel={t('workspace.config.reset')}
        confirmVariant="danger"
        loading={resetting}
        onConfirm={handleReset}
        onCancel={() => setShowResetConfirm(false)}
      />
    </>
  )
}
