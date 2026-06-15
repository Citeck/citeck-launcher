import { useCallback, useEffect, useRef, useState } from 'react'
import { ChevronDown, Loader2, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { Modal, ModalField } from './Modal'
import { Select } from './Select'
import {
  ApiError,
  activateWorkspace,
  createWorkspace,
  deleteWorkspace,
  listWorkspaces,
  postWorkspaceUpdate,
  updateWorkspace,
} from '../lib/api'
import type { WorkspaceCreateDto, WorkspaceDto, WorkspaceUpdateDto } from '../lib/types'
import { SecretPicker } from './SecretPicker'
import { GitPullErrorDialog } from './GitPullErrorDialog'
import { extractHost, isAuthShapedGitError } from '../lib/giturl'
import { useTranslation } from '../lib/i18n'
import { useIsDesktop } from '../lib/daemonStatus'
import { toast } from '../lib/toast'
import { showError } from '../lib/errorModal'
import { ConfirmModal } from './ConfirmModal'

interface WorkspaceSelectorProps {
  /** Current active workspace id (from /daemon/status). */
  activeId: string
  /** Called after a successful activate/create/delete so the parent can refetch. */
  onChanged: () => void
}

type FormMode = 'create' | { kind: 'edit'; ws: WorkspaceDto }

/**
 * Workspace picker for the Welcome screen (desktop-only multi-workspace).
 *
 * Renders as a small dropdown showing the active workspace name; opens a list
 * of all workspaces + actions (create, edit, delete). In server mode the
 * /workspaces endpoint returns 404 and listWorkspaces resolves to `[]` — the
 * component then renders nothing.
 */
export function WorkspaceSelector({ activeId, onChanged }: WorkspaceSelectorProps) {
  const { t } = useTranslation()
  const isDesktop = useIsDesktop()
  const [workspaces, setWorkspaces] = useState<WorkspaceDto[]>([])
  const [open, setOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const [formMode, setFormMode] = useState<FormMode | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<WorkspaceDto | null>(null)
  const [deleteLoading, setDeleteLoading] = useState(false)
  const [deleteError, setDeleteError] = useState('')
  const [forceUpdating, setForceUpdating] = useState(false)
  // Workspace-switch git failure (WS_REPO_SYNC_FAILED / auth-shaped): the
  // daemon stays on the previous workspace, so surface the actionable
  // GitPullErrorDialog targeting the FAILED workspace — its token section
  // lets the user fix/replace the secret and retry the switch in place.
  const [gitError, setGitError] = useState<{ ws: WorkspaceDto; message: string } | null>(null)
  const containerRef = useRef<HTMLDivElement>(null)

  const refresh = useCallback(async () => {
    try {
      const list = await listWorkspaces()
      setWorkspaces(list)
    } catch (e) {
      // Daemon down / server mode — silently empty.
      setWorkspaces([])
      console.error('listWorkspaces failed', e)
    }
  }, [])

  useEffect(() => {
    // Intentional: load-on-mount workspace list (desktop only); not a
    // cascading render.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (isDesktop) refresh()
  }, [isDesktop, refresh])

  // Close the dropdown when clicking outside.
  useEffect(() => {
    if (!open) return
    function onDocClick(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', onDocClick)
    return () => document.removeEventListener('mousedown', onDocClick)
  }, [open])

  // Server mode is single-workspace by design — hide the picker entirely.
  // `isDesktop === null` means daemon status hasn't resolved yet; render
  // nothing rather than flash the selector and then collapse it.
  if (!isDesktop) {
    return null
  }

  const active = workspaces.find((w) => w.id === activeId) ?? workspaces.find((w) => w.active)
  const activeName = active?.name || active?.id || activeId || t('welcome.workspace.label')

  async function handleActivate(ws: WorkspaceDto) {
    if (ws.id === activeId) {
      setOpen(false)
      return
    }
    setLoading(true)
    try {
      await activateWorkspace(ws.id)
      toast(t('welcome.workspace.switched', { name: ws.name || ws.id }), 'success')
      setOpen(false)
      onChanged()
      await refresh()
    } catch (e) {
      const err = e instanceof Error ? e : new Error(String(e))
      const code = e instanceof ApiError ? e.code : ''
      // Repo-sync / auth failures get the actionable git dialog (fix the
      // token and retry); anything else keeps the generic error modal.
      if (code === 'WS_REPO_SYNC_FAILED' || isAuthShapedGitError(err.message)) {
        setOpen(false)
        setGitError({ ws, message: err.message })
      } else {
        showError({
          title: t('welcome.workspace.label'),
          message: t('welcome.workspace.switchFailed', { error: err.message }),
          details: err.stack,
        })
      }
    } finally {
      setLoading(false)
    }
  }

  async function handleForceUpdate(ws: WorkspaceDto) {
    // The /workspace/update endpoint operates on the active workspace only —
    // Kotlin parity (WelcomeScreen.kt "Force Update" was tied to the active
    // workspace TextButton). For inactive items the button is disabled.
    if (ws.id !== activeId) return
    setForceUpdating(true)
    setOpen(false)
    try {
      await postWorkspaceUpdate()
      toast(t('welcome.workspace.updateSuccess'), 'success')
      onChanged()
    } catch (e) {
      const err = e instanceof Error ? e : new Error(String(e))
      showError({
        title: t('welcome.workspace.forceUpdate'),
        message: t('welcome.workspace.updateFailed', { error: err.message }),
        details: err.stack,
      })
    } finally {
      setForceUpdating(false)
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return
    setDeleteLoading(true)
    setDeleteError('')
    try {
      await deleteWorkspace(deleteTarget.id)
      setDeleteTarget(null)
      await refresh()
      onChanged()
    } catch (e) {
      setDeleteError(e instanceof Error ? e.message : String(e))
    } finally {
      setDeleteLoading(false)
    }
  }

  return (
    <div className="flex items-center gap-0.5" ref={containerRef}>
      <div className="relative">
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          className="flex items-center gap-1 rounded px-2 py-0.5 text-xs text-muted-foreground hover:bg-muted/40 hover:text-foreground"
        >
          <span>{t('welcome.workspace.label')}: {activeName}</span>
          {loading ? <Loader2 size={12} className="animate-spin" /> : <ChevronDown size={12} />}
        </button>

      {open && (
        <div className="absolute left-0 top-full z-50 mt-1 w-72 rounded-md border border-border bg-popover shadow-lg">
          <ul className="max-h-72 overflow-auto py-1">
            {workspaces.map((ws) => (
              <li key={ws.id} className="group flex items-center justify-between px-2 py-1 hover:bg-muted/40">
                <button
                  type="button"
                  className="flex-1 flex items-center gap-1.5 text-left text-xs text-foreground"
                  onClick={() => handleActivate(ws)}
                >
                  <input
                    type="checkbox"
                    readOnly
                    checked={ws.id === activeId}
                    aria-label={t('welcome.workspace.label')}
                    className="rounded border-border pointer-events-none shrink-0"
                  />
                  <span className="truncate">
                    {ws.name || ws.id}
                    <span className="ml-1 text-muted-foreground">({ws.namespaces})</span>
                  </span>
                </button>
                <div className="flex gap-1 opacity-0 group-hover:opacity-100">
                  <button
                    type="button"
                    aria-label={t('welcome.workspace.edit')}
                    title={t('welcome.workspace.edit')}
                    className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-background/60"
                    onClick={() => { setFormMode({ kind: 'edit', ws }); setOpen(false) }}
                  >
                    <Pencil size={11} />
                  </button>
                  <button
                    type="button"
                    aria-label={t('welcome.workspace.delete')}
                    title={t('welcome.workspace.delete')}
                    disabled={ws.id === activeId}
                    className="p-1 rounded text-muted-foreground hover:text-destructive hover:bg-background/60 disabled:opacity-40 disabled:hover:text-muted-foreground"
                    onClick={() => { setDeleteTarget(ws); setOpen(false) }}
                  >
                    <Trash2 size={11} />
                  </button>
                </div>
              </li>
            ))}
          </ul>
          <div className="border-t border-border">
            <button
              type="button"
              className="flex w-full items-center gap-1.5 px-2 py-1.5 text-xs text-foreground hover:bg-muted/40"
              onClick={() => { setFormMode('create'); setOpen(false) }}
            >
              <Plus size={12} />
              {t('welcome.workspace.create')}
            </button>
          </div>
        </div>
      )}
      </div>

      {/* Force-update the ACTIVE workspace — moved out of the dropdown rows to
          sit right of the selector on the top panel (Kotlin parity: active-
          workspace Force Update). */}
      <button
        type="button"
        aria-label={t('welcome.workspace.forceUpdate')}
        title={t('welcome.workspace.forceUpdate')}
        disabled={!active || forceUpdating}
        className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted/40 disabled:opacity-40 disabled:hover:text-muted-foreground"
        onClick={() => { if (active) void handleForceUpdate(active) }}
      >
        {forceUpdating ? <Loader2 size={12} className="animate-spin" /> : <RefreshCw size={12} />}
      </button>

      {formMode && (
        <WorkspaceFormDialog
          mode={formMode}
          onClose={() => setFormMode(null)}
          onSaved={async () => {
            setFormMode(null)
            await refresh()
            onChanged()
          }}
        />
      )}

      <ConfirmModal
        open={!!deleteTarget}
        title={t('welcome.workspace.delete')}
        message={t('welcome.workspace.deleteConfirm', { name: deleteTarget?.name || deleteTarget?.id || '' })}
        confirmLabel={t('common.delete')}
        confirmVariant="danger"
        loading={deleteLoading}
        error={deleteError}
        onConfirm={handleDelete}
        onCancel={() => { setDeleteTarget(null); setDeleteError('') }}
      />

      {/* Workspace-switch repo failure: actionable git dialog bound to the
          TARGET workspace (the daemon reverted to the previous one). Retry
          re-attempts the activation after the token section's fixes. */}
      {gitError && (
        <GitPullErrorDialog
          open
          repoUrl={gitError.ws.repoUrl}
          errorMessage={gitError.message}
          skipAvailable={false}
          cancelAvailable
          workspaceId={gitError.ws.id}
          onDecide={(d) => {
            const target = gitError.ws
            setGitError(null)
            if (d === 'retry') void handleActivate(target)
          }}
        />
      )}
    </div>
  )
}

interface WorkspaceFormDialogProps {
  mode: FormMode
  onClose: () => void
  onSaved: () => void
}

function WorkspaceFormDialog({ mode, onClose, onSaved }: WorkspaceFormDialogProps) {
  const { t } = useTranslation()
  const isEdit = mode !== 'create'
  const existing = isEdit ? mode.ws : null

  // ID is server-generated (opaque random slug) — never exposed in the UI.
  // Name is the user-facing reference info.
  const [name, setName] = useState(existing?.name ?? '')
  const [repoUrl, setRepoUrl] = useState(existing?.repoUrl ?? '')
  const [repoBranch, setRepoBranch] = useState(existing?.repoBranch ?? 'main')
  const [repoPullPeriod, setRepoPullPeriod] = useState(existing?.repoPullPeriod ?? 'PT2H')
  const [authType, setAuthType] = useState<'NONE' | 'TOKEN'>((existing?.authType as 'NONE' | 'TOKEN') ?? 'NONE')
  // Token-secret picker (authType=TOKEN). Edit mode preselects the currently
  // linked secret; the token value itself is write-only and never shown.
  // Create-new happens inside the picker's own modal — by submit time the
  // secret already exists and `secretId` is its id.
  const [secretId, setSecretId] = useState(existing?.secretId ?? '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!name.trim() || !repoUrl.trim()) {
      setError(t('welcome.workspace.form.required'))
      return
    }
    // A NEW workspace with TOKEN auth needs a secret up front. Edit mode
    // tolerates no selection — absent secretId means "unchanged", which
    // keeps the legacy ws:<id>:repo secret lookup working.
    if (authType === 'TOKEN' && !isEdit && !secretId) {
      setError(t('welcome.workspace.form.tokenRequired'))
      return
    }
    setBusy(true)
    setError('')
    try {
      if (isEdit) {
        const update: WorkspaceUpdateDto = {
          name: name.trim(),
          repoUrl: repoUrl.trim(),
          repoBranch: repoBranch.trim() || undefined,
          repoPullPeriod: repoPullPeriod.trim() || undefined,
          authType,
        }
        if (authType === 'TOKEN') {
          // Absent field = unchanged (legacy ws:<id>:repo secrets keep
          // working when the user didn't touch the picker).
          if (secretId) update.secretId = secretId
        } else {
          // Switching to NONE unlinks the secret ('' = explicit unlink).
          update.secretId = ''
        }
        await updateWorkspace(existing!.id, update)
      } else {
        const create: WorkspaceCreateDto = {
          name: name.trim(),
          repoUrl: repoUrl.trim(),
          repoBranch: repoBranch.trim() || undefined,
          repoPullPeriod: repoPullPeriod.trim() || undefined,
          authType,
        }
        if (authType === 'TOKEN' && secretId) create.secretId = secretId
        await createWorkspace(create)
      }
      onSaved()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const title = isEdit ? t('welcome.workspace.edit') : t('welcome.workspace.create')
  const inputCls = 'w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary'

  return (
    <Modal
      open
      title={title}
      onClose={onClose}
      onSubmit={handleSubmit}
      footer={
        <>
          <button
            type="button"
            className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50"
            onClick={onClose}
            disabled={busy}
          >
            {t('common.cancel')}
          </button>
          <button
            type="submit"
            disabled={busy}
            className="rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-xs font-medium hover:bg-primary/90 disabled:opacity-50"
          >
            {busy ? <Loader2 size={12} className="animate-spin" /> : t('common.save')}
          </button>
        </>
      }
    >
      <ModalField label={t('welcome.workspace.form.name')} required>
        <input
          type="text"
          required
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
          className={inputCls}
        />
      </ModalField>
      <ModalField label={t('welcome.workspace.form.repoUrl')} required>
        <input
          type="url"
          required
          value={repoUrl}
          onChange={(e) => setRepoUrl(e.target.value)}
          placeholder="https://github.com/Citeck/launcher-workspace.git"
          className={inputCls}
        />
      </ModalField>
      <ModalField label={t('welcome.workspace.form.repoBranch')}>
        <input
          type="text"
          value={repoBranch}
          onChange={(e) => setRepoBranch(e.target.value)}
          className={inputCls}
        />
      </ModalField>
      <ModalField label={t('welcome.workspace.form.repoPullPeriod')}>
        <input
          type="text"
          value={repoPullPeriod}
          onChange={(e) => setRepoPullPeriod(e.target.value)}
          placeholder="PT2H"
          className={inputCls}
        />
      </ModalField>
      <ModalField label={t('welcome.workspace.form.authType')}>
        <Select
          value={authType}
          options={[
            { value: 'NONE', label: t('welcome.workspace.form.authType.none') },
            { value: 'TOKEN', label: t('welcome.workspace.form.authType.token') },
          ]}
          onChange={(v) => setAuthType(v as 'NONE' | 'TOKEN')}
          required
        />
      </ModalField>
      {authType === 'TOKEN' && (
        <SecretPicker
          value={secretId}
          onChange={setSecretId}
          defaultNewName={extractHost(repoUrl)}
          disabled={busy}
        />
      )}
      {error && (
        <div className="rounded-md bg-destructive/10 border border-destructive/20 px-3 py-2 text-xs text-destructive">
          {error}
        </div>
      )}
    </Modal>
  )
}
