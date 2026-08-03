import { useCallback, useEffect, useRef, useState } from 'react'
import { ChevronDown, FileCode2, Loader2, Pencil, Plus, RefreshCw, Settings, Trash2 } from 'lucide-react'
import {
  ApiError,
  activateWorkspace,
  deleteWorkspace,
  listWorkspaces,
  postWorkspaceUpdate,
} from '../lib/api'
import type { WorkspaceDto } from '../lib/types'
import { GitPullErrorDialog } from './GitPullErrorDialog'
import { isAuthShapedGitError } from '../lib/giturl'
import { useTranslation } from '../lib/i18n'
import { useIsDesktop } from '../lib/daemonStatus'
import { toast } from '../lib/toast'
import { showError } from '../lib/errorModal'
import { ConfirmModal } from './ConfirmModal'
import { WorkspaceConfigDialog } from './WorkspaceConfigDialog'
import { WorkspaceFormDialog, type FormMode } from './WorkspaceFormDialog'

interface WorkspaceSelectorProps {
  /** Current active workspace id (from /daemon/status). */
  activeId: string
  /** Called after a successful activate/create/delete so the parent can refetch. */
  onChanged: () => void
}

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
  const [configOpen, setConfigOpen] = useState(false)
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
      // Refresh on BOTH success and failure: on a failed activation the daemon
      // stays on the previous workspace, but a just-created (and now inactive)
      // workspace must still appear in the list — otherwise it would be missing
      // from the dropdown until the next refresh trigger.
      await refresh()
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
                {/* Always visible (only dimmed until hover): behind `opacity-0`
                    the pencil was invisible unless the pointer happened to land
                    on the row, and users reported never finding the workspace
                    git settings at all. */}
                <div className="flex gap-1 opacity-70 group-hover:opacity-100">
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
            {/* Named entry into the TYPED form for the active workspace — the
                per-row pencil is easy to miss, and "settings" is the word users
                actually look for when hunting for the git repo/branch/token. */}
            <button
              type="button"
              disabled={!active}
              className="flex w-full items-center gap-1.5 px-2 py-1.5 text-xs text-foreground hover:bg-muted/40 disabled:opacity-40"
              onClick={() => { if (active) { setFormMode({ kind: 'edit', ws: active }); setOpen(false) } }}
            >
              <Settings size={12} />
              {t('config.workspace.settings')}
            </button>
            {/* Raw workspace-v1.yml — demoted from the always-visible gear to a
                clearly-labelled power-user row, so the prominent affordance no
                longer dumps a first-time user into a YAML editor. */}
            <button
              type="button"
              disabled={!active}
              className="flex w-full items-center gap-1.5 px-2 py-1.5 text-xs text-muted-foreground hover:bg-muted/40 hover:text-foreground disabled:opacity-40"
              onClick={() => { if (active) { setConfigOpen(true); setOpen(false) } }}
            >
              <FileCode2 size={12} />
              {t('workspace.config.rawEdit')}
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

      {/* Settings for the ACTIVE workspace. This used to open the raw
          workspace-v1.yml editor: it is the most prominent affordance next to
          the picker, so users looking for the git repo/branch/token clicked it,
          got YAML, and gave up. It now opens the typed form; the YAML editor
          moved to a labelled row inside the dropdown. */}
      <button
        type="button"
        aria-label={t('config.workspace.editTitle')}
        title={t('config.workspace.editTitle')}
        disabled={!active}
        className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted/40 disabled:opacity-40 disabled:hover:text-muted-foreground focus:outline-none focus-visible:outline-none"
        onClick={() => { if (active) setFormMode({ kind: 'edit', ws: active }) }}
      >
        <Settings size={12} />
      </button>

      {configOpen && active && (
        <WorkspaceConfigDialog
          workspaceId={active.id}
          onClose={() => setConfigOpen(false)}
          onSaved={onChanged}
        />
      )}

      {formMode && (
        <WorkspaceFormDialog
          mode={formMode}
          onClose={() => setFormMode(null)}
          onSaved={async (createdWs) => {
            setFormMode(null)
            // Creating a workspace auto-switches to it (Kotlin 1.x parity: the
            // create flow set the new entity as the selected workspace). Reuse
            // handleActivate so a failed first sync surfaces the actionable git
            // dialog; it refreshes the list itself (success or failure). Edit
            // keeps the active workspace unchanged — just refresh + notify.
            if (createdWs) {
              await handleActivate(createdWs)
            } else {
              await refresh()
              onChanged()
            }
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
