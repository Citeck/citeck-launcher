import { useCallback, useEffect, useRef, useState } from 'react'
import { listWorkspaces, postGitSkipPull, updateWorkspace } from '../lib/api'
import { extractHost, isAuthShapedGitError } from '../lib/giturl'
import { useTranslation } from '../lib/i18n'
import type { SecretMetaDto, WorkspaceDto } from '../lib/types'
import { SecretPicker } from './SecretPicker'
import { SecretEditDialog } from './SecretEditDialog'
import { needsWorkspaceRelink, workspaceSecretInUse } from '../lib/secretPicker'

export type GitPullDecision = 'retry' | 'skip' | 'cancel'

interface GitPullErrorDialogProps {
  open: boolean
  repoUrl: string
  errorMessage: string
  skipAvailable: boolean
  cancelAvailable: boolean
  /**
   * Target workspace for the auth-error section. When set AND the error is
   * auth-shaped, the dialog explains in plain words which secret is in use
   * and offers: edit that secret (write-only), pick a different one, or
   * create a new one — then "Save and Retry" relinks the workspace if the
   * selection changed and drives onDecide('retry'). Absent in contexts with
   * no workspace to bind to.
   */
  workspaceId?: string
  onDecide: (d: GitPullDecision) => void
}

/**
 * Port of Kotlin's `GitPullErrorDialog`.
 *
 * Surfaces a recoverable git pull failure with three actions:
 *  - Retry — re-attempt the pull
 *  - Skip — proceed using the last successful clone (only when one exists,
 *    Kotlin: skipAvailable=true). The host portion of `repoUrl` is posted
 *    to /api/v1/git/skip-pull so the daemon suppresses pull operations
 *    against the same host for the next hour — sibling bundle / workspace
 *    repos hosted there won't re-prompt either (Kotlin parity —
 *    `skipPullForRepoDecisionAt` map).
 *  - Cancel — abort the higher-level operation (only when allowed by caller).
 *
 * For auth-shaped failures with a known target workspace it additionally
 * renders the actionable token section (Kotlin 1.x prompted for the token
 * modally at clone time) — see the `workspaceId` prop.
 */
export function GitPullErrorDialog({ open, repoUrl, errorMessage, skipAvailable, cancelAvailable, workspaceId, onDecide }: GitPullErrorDialogProps) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)
  // Selected secret id in the picker ('' = nothing picked yet).
  const [selection, setSelection] = useState('')
  // GIT_TOKEN secrets — fed by the picker's own fetch (single source).
  const [secrets, setSecrets] = useState<SecretMetaDto[]>([])
  const [workspaces, setWorkspaces] = useState<WorkspaceDto[]>([])
  const [editTarget, setEditTarget] = useState<SecretMetaDto | null>(null)
  // Bumped after an edit-save so the picker refetches (names may change).
  const [reloadKey, setReloadKey] = useState(0)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')
  // Preselect the workspace's current secret only once per open — list
  // refreshes (create/delete/edit) must not clobber the user's choice.
  const preselectedRef = useRef(false)

  useEffect(() => {
    const d = dialogRef.current
    if (!d) return
    if (open && !d.open) d.showModal()
    else if (!open && d.open) d.close()
  }, [open])

  // Reset the token section whenever the dialog (re)opens — a stale selection
  // made for a previous failure must not leak into the next one.
  useEffect(() => {
    if (!open) return
    // Intentional: state reset on dialog open; not a cascading render.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setSelection('')
    setSaveError('')
    setEditTarget(null)
    preselectedRef.current = false
  }, [open])

  const showTokenSection = !!workspaceId && isAuthShapedGitError(errorMessage)

  // Resolve the workspace whose repo failed, so the section can SAY which
  // secret is in use. Best-effort: server mode / older daemons yield [].
  useEffect(() => {
    if (!open || !showTokenSection) return
    let cancelled = false
    listWorkspaces()
      .then((list) => { if (!cancelled) setWorkspaces(list) })
      .catch(() => {})
    return () => { cancelled = true }
  }, [open, showTokenSection])

  const handleSecretsChange = useCallback((list: SecretMetaDto[]) => setSecrets(list), [])

  // Secret in use: the workspace's linked secretId, or the legacy
  // ws:<id>:repo secret when authType=TOKEN without a link.
  const ws = workspaces.find((w) => w.id === workspaceId)
  const currentSecretId = workspaceSecretInUse(ws)
  const currentSecret = secrets.find((s) => s.id === currentSecretId)

  // Preselect the in-use secret once it resolves against the loaded list.
  // A dangling reference (deleted secret) never preselects — the user must
  // pick another or create a new one.
  useEffect(() => {
    if (!open || preselectedRef.current || !currentSecret) return
    preselectedRef.current = true
    // Intentional: one-shot preselect after async loads; not a cascade.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setSelection(currentSecret.id)
  }, [open, currentSecret])

  // Skip handler: fire-and-forget post to the daemon so the host-level
  // suppression takes effect before the caller's retry path re-evaluates.
  // Errors are swallowed (best-effort) — the user already made a decision,
  // and worst case the next pull will just re-prompt.
  const handleSkip = () => {
    const host = extractHost(repoUrl)
    if (host) {
      postGitSkipPull(host, 3600).catch((err) => {
        // Surface in console so QA can spot daemon-side issues, but never
        // block the UI on this side-effect.

        console.warn('git skip-pull request failed:', err)
      })
    }
    onDecide('skip')
  }

  // Save-and-retry: relink the workspace only when the user picked a
  // DIFFERENT secret; if only the secret's VALUE was edited (same id), the
  // daemon resolves the fresh value by id and a plain retry suffices.
  async function handleSaveAndRetry() {
    if (!workspaceId || !selection) return
    setSaving(true)
    setSaveError('')
    try {
      if (needsWorkspaceRelink(currentSecretId, selection)) {
        await updateWorkspace(workspaceId, { secretId: selection })
      }
      onDecide('retry')
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <dialog
      ref={dialogRef}
      className="fixed inset-0 z-50 m-auto max-w-lg w-full rounded-lg border border-border bg-card p-0 text-foreground shadow-xl"
    >
      <div className="p-6">
        <h2 className="text-lg font-semibold mb-3">{t('gitPullError.title')}</h2>
        <p className="text-xs text-destructive whitespace-pre-wrap mb-3">{errorMessage}</p>
        <p className="text-sm font-mono text-muted-foreground break-all mb-3">{repoUrl}</p>
        <p className="text-xs text-muted-foreground mb-4">
          {skipAvailable ? t('gitPullError.canSkip') : t('gitPullError.cannotSkip')}
        </p>
        {showTokenSection && (
          <div className="mb-4 rounded-md border border-border bg-background/40 p-3 space-y-3">
            {/* Plain-language explanation + WHICH secret is in use, so a
                non-expert knows what to fix. */}
            <p className="text-xs text-muted-foreground">{t('gitPullError.authExplain')}</p>
            <p className="text-xs text-foreground">
              {!currentSecretId
                ? t('gitPullError.noSecret')
                : currentSecret
                  ? t('gitPullError.secretInUse', { name: currentSecret.name || currentSecret.id })
                  : t('gitPullError.secretInUseMissing', { id: currentSecretId })}
            </p>
            <SecretPicker
              value={selection}
              onChange={setSelection}
              defaultNewName={extractHost(repoUrl)}
              disabled={saving}
              onEditRequest={setEditTarget}
              onSecretsChange={handleSecretsChange}
              reloadKey={reloadKey}
            />
            {saveError && (
              <div className="rounded-md bg-destructive/10 border border-destructive/20 px-3 py-2 text-xs text-destructive">
                {saveError}
              </div>
            )}
            <div className="flex justify-end gap-2">
              {currentSecret && (
                <button
                  type="button"
                  className="rounded-md border border-border px-3 py-1.5 text-sm hover:bg-muted disabled:opacity-50"
                  disabled={saving}
                  onClick={() => setEditTarget(currentSecret)}
                >
                  {t('gitPullError.editToken')}
                </button>
              )}
              <button
                type="button"
                className="rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-sm font-medium hover:bg-primary/90 disabled:opacity-50"
                disabled={saving || !selection}
                onClick={handleSaveAndRetry}
              >
                {t('gitPullError.saveAndRetry')}
              </button>
            </div>
          </div>
        )}
        <div className="flex justify-end gap-2">
          {cancelAvailable && (
            <button
              type="button"
              className="rounded-md border border-border px-3 py-1.5 text-sm hover:bg-muted"
              onClick={() => onDecide('cancel')}
              disabled={saving}
            >
              {t('common.cancel')}
            </button>
          )}
          {skipAvailable && (
            <button
              type="button"
              className="rounded-md border border-border px-3 py-1.5 text-sm hover:bg-muted"
              onClick={handleSkip}
              disabled={saving}
            >
              {t('gitPullError.skip')}
            </button>
          )}
          <button
            type="button"
            className="rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-sm font-medium hover:bg-primary/90 disabled:opacity-50"
            onClick={() => onDecide('retry')}
            disabled={saving}
          >
            {t('gitPullError.retry')}
          </button>
        </div>
      </div>

      {/* Shared write-only edit dialog: serves both the "Edit token" action
          and the picker rows' edit icons (delegated via onEditRequest). */}
      <SecretEditDialog
        open={!!editTarget}
        secret={editTarget}
        onClose={() => setEditTarget(null)}
        onSaved={() => {
          setEditTarget(null)
          setReloadKey((k) => k + 1)
        }}
      />
    </dialog>
  )
}
