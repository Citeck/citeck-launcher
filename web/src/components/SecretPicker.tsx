import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ChevronDown, Loader2, Pencil, Plus, Trash2 } from 'lucide-react'
import { Modal, ModalField } from './Modal'
import { ConfirmModal } from './ConfirmModal'
import { SecretEditDialog } from './SecretEditDialog'
import { getSecrets, deleteSecret, listWorkspaces } from '../lib/api'
import type { SecretMetaDto, WorkspaceDto } from '../lib/types'
import {
  createGitTokenSecret,
  createRegistrySecret,
  secretDeleteMessage,
  workspacesUsingSecret,
} from '../lib/secretPicker'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'

interface SecretPickerProps {
  /** Selected secret id ('' = nothing selected). */
  value: string
  onChange: (secretId: string) => void
  /** Secret type the picker lists and creates. Defaults to GIT_TOKEN (one
   *  token field); REGISTRY_AUTH adds username + password. */
  secretType?: 'GIT_TOKEN' | 'REGISTRY_AUTH'
  /** When set, the list is filtered to secrets tagged with this exact host
   *  (with a "show all" escape), and the create form binds the new secret to
   *  it. Used by the registry-credentials flow so creds are reused per host. */
  host?: string
  /** Suggested name for the create-new modal, e.g. the repo/registry host. */
  defaultNewName?: string
  disabled?: boolean
  /** Delegate row-edit clicks to the parent (one shared SecretEditDialog
   *  instance, e.g. GitPullErrorDialog's) instead of the picker's internal
   *  dialog. */
  onEditRequest?: (secret: SecretMetaDto) => void
  /** Fires whenever the picker (re)loads the GIT_TOKEN secret list, so the
   *  parent can resolve names against the same single fetch. */
  onSecretsChange?: (secrets: SecretMetaDto[]) => void
  /** Bump to force a list reload (e.g. after the parent edited a secret). */
  reloadKey?: number
}

// Mirror Select.tsx metrics so the two dropdowns feel identical.
const ITEM_HEIGHT = 30
const MAX_VISIBLE_ITEMS = 8

interface PopupPos {
  left: number
  width: number
  top?: number
  bottom?: number
}

/**
 * Reusable GIT_TOKEN secret picker: ONE dropdown over existing token secrets
 * (meta only — values are write-only and never displayed) with a final
 * "Add new…" entry that opens a create-secret MODAL (no inline form
 * expansion). Each secret row carries hover actions: edit (shared write-only
 * SecretEditDialog) and delete (ConfirmModal with a "used by workspaces"
 * warning). Used by the workspace form (authType=TOKEN) and the
 * GitPullErrorDialog auth-error section, so one GitLab token can be shared
 * by several customer workspaces.
 *
 * Built as a custom button+popover (not the generic Select) because rows
 * host inline action buttons; popup positioning/portal logic mirrors
 * Select.tsx — portal into the nearest OPEN <dialog> ancestor so the popup
 * survives the browser top layer, flip above when there's no room below,
 * Escape closes, outside click closes.
 *
 * Save-flow logic (slug/id generation, payload mapping, relink decisions)
 * lives in lib/secretPicker.ts — this file is the presentation only.
 */
export function SecretPicker({
  value,
  onChange,
  secretType = 'GIT_TOKEN',
  host,
  defaultNewName = '',
  disabled = false,
  onEditRequest,
  onSecretsChange,
  reloadKey = 0,
}: SecretPickerProps) {
  const isRegistry = secretType === 'REGISTRY_AUTH'
  const { t } = useTranslation()
  const [secrets, setSecrets] = useState<SecretMetaDto[]>([])
  // Workspace references power the delete-confirm warning (desktop only —
  // [] in server mode / on failure).
  const [workspaces, setWorkspaces] = useState<WorkspaceDto[]>([])

  // Popup state (mirrors Select.tsx).
  const [open, setOpen] = useState(false)
  const [activeIdx, setActiveIdx] = useState(-1)
  const [popupPos, setPopupPos] = useState<PopupPos | null>(null)
  const [portalEl, setPortalEl] = useState<HTMLElement | null>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const popupRef = useRef<HTMLDivElement>(null)

  // "Add new…" modal state.
  const [createOpen, setCreateOpen] = useState(false)
  const [newName, setNewName] = useState('')
  const [newToken, setNewToken] = useState('')
  const [newUser, setNewUser] = useState('') // REGISTRY_AUTH only
  const [createBusy, setCreateBusy] = useState(false)
  const [createError, setCreateError] = useState('')
  // When a host filter is active, show only secrets tagged with it; this
  // toggle reveals the rest so a credential from another host can be reused.
  const [showAllHosts, setShowAllHosts] = useState(false)
  // Reset the escape when the host changes: the picker stays mounted across
  // dialog opens (Modal keeps children in the DOM), so without this a "show
  // all" from a previous host would silently bypass the next host's filter.
  // Adjusting state during render is React's recommended alternative to a
  // reset effect (https://react.dev/learn/you-might-not-need-an-effect).
  const [prevHost, setPrevHost] = useState(host)
  if (host !== prevHost) {
    setPrevHost(host)
    setShowAllHosts(false)
  }

  // Internal edit/delete dialogs (edit is delegated via onEditRequest when set).
  const [editTarget, setEditTarget] = useState<SecretMetaDto | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<SecretMetaDto | null>(null)
  const [deleteBusy, setDeleteBusy] = useState(false)
  const [deleteError, setDeleteError] = useState('')

  // Keep the parent callback in a ref so reload() stays identity-stable and
  // the load effect doesn't refire when a parent re-renders.
  const onSecretsChangeRef = useRef(onSecretsChange)
  useEffect(() => { onSecretsChangeRef.current = onSecretsChange })

  const reload = useCallback(async () => {
    try {
      const list = (await getSecrets()).filter((s) => s.type === secretType)
      setSecrets(list)
      onSecretsChangeRef.current?.(list)
    } catch (e) {
      // List unavailable (locked secret store / daemon hiccup) — the
      // create-new path still works, so don't block the picker on this.
      console.warn('SecretPicker: failed to load secrets', e)
    }
  }, [secretType])

  useEffect(() => {
    // reload() only sets state AFTER an awaited fetch — not a synchronous
    // set-in-effect, so the cascading-render heuristic is a false positive.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void reload()
    // Best-effort: 404 in server mode resolves to []; ignore other failures.
    listWorkspaces().then(setWorkspaces).catch(() => {})
  }, [reload, reloadKey])

  // Outside click / reposition handling (mirrors Select.tsx — the popup is
  // fixed-positioned from a snapshot of the trigger rect, so any ancestor
  // scroll/resize invalidates the anchor and we just close).
  useEffect(() => {
    if (!open) return
    function onDocClick(e: MouseEvent) {
      const target = e.target as Node
      if (rootRef.current?.contains(target) || popupRef.current?.contains(target)) return
      setOpen(false)
    }
    function onReposition(e: Event) {
      if (e.type === 'scroll' && e.target instanceof Node && popupRef.current?.contains(e.target)) return
      setOpen(false)
    }
    document.addEventListener('mousedown', onDocClick)
    window.addEventListener('resize', onReposition)
    window.addEventListener('scroll', onReposition, true)
    return () => {
      document.removeEventListener('mousedown', onDocClick)
      window.removeEventListener('resize', onReposition)
      window.removeEventListener('scroll', onReposition, true)
    }
  }, [open])

  const selected = secrets.find((s) => s.id === value)
  const missing = !!value && !selected
  // When a host filter is active (and not overridden), show only secrets tagged
  // with that host; `selected` still resolves against the full list so the
  // trigger label is correct even for an out-of-filter selection.
  const filteringByHost = !!host && !showAllHosts
  const visible = filteringByHost ? secrets.filter((s) => s.host === host) : secrets
  const hiddenCount = secrets.length - visible.length
  // Rows: one per visible secret + the trailing "Add new…" entry.
  const rowCount = visible.length + 1

  function openPopup() {
    if (!triggerRef.current) return
    // Portal into the nearest OPEN <dialog> ancestor (browser top layer)
    // when there is one; otherwise into <body> — same reasoning as Select.
    const dlg = triggerRef.current.closest('dialog')
    setPortalEl(dlg && dlg.open ? dlg : document.body)
    const rect = triggerRef.current.getBoundingClientRect()
    const popupHeight = Math.min(rowCount, MAX_VISIBLE_ITEMS) * ITEM_HEIGHT + 4
    const spaceBelow = window.innerHeight - rect.bottom
    const spaceAbove = rect.top
    const up = spaceBelow < popupHeight && spaceAbove > spaceBelow
    setPopupPos({
      left: rect.left,
      width: rect.width,
      top: up ? undefined : rect.bottom + 4,
      bottom: up ? window.innerHeight - rect.top + 4 : undefined,
    })
  }

  function pick(id: string) {
    onChange(id)
    setOpen(false)
    triggerRef.current?.focus()
  }

  function openCreate() {
    setOpen(false)
    setNewName(defaultNewName)
    setNewToken('')
    setNewUser('')
    setCreateError('')
    setCreateOpen(true)
  }

  function requestEdit(s: SecretMetaDto) {
    setOpen(false)
    if (onEditRequest) onEditRequest(s)
    else setEditTarget(s)
  }

  function requestDelete(s: SecretMetaDto) {
    setOpen(false)
    setDeleteError('')
    setDeleteTarget(s)
  }

  function onKeyDown(e: React.KeyboardEvent) {
    if (disabled) return
    if (!open) {
      if (e.key === 'Enter' || e.key === ' ' || e.key === 'ArrowDown') {
        e.preventDefault()
        openPopup()
        setOpen(true)
        const selIdx = visible.findIndex((s) => s.id === value)
        setActiveIdx(selIdx >= 0 ? selIdx : 0)
      }
      return
    }
    if (e.key === 'Escape') {
      // preventDefault also stops an enclosing <dialog> from treating the
      // Escape as its own cancel — closing the popup must not close the form.
      e.preventDefault()
      setOpen(false)
      triggerRef.current?.focus()
    } else if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActiveIdx((i) => Math.min(i + 1, rowCount - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActiveIdx((i) => Math.max(i - 1, 0))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      if (activeIdx >= 0 && activeIdx < visible.length) pick(visible[activeIdx].id)
      else if (activeIdx === visible.length) openCreate()
    }
  }

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault()
    const name = newName.trim()
    if (!name || !newToken || (isRegistry && !newUser.trim())) {
      setCreateError(t('secretPicker.required'))
      return
    }
    setCreateBusy(true)
    setCreateError('')
    try {
      const id = isRegistry
        ? await createRegistrySecret(name, newUser, newToken, host ?? '')
        : await createGitTokenSecret(name, newToken)
      setCreateOpen(false)
      setNewToken('')
      setNewUser('')
      await reload()
      // The fresh secret becomes the selected dropdown value.
      onChange(id)
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : String(err))
    } finally {
      setCreateBusy(false)
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return
    setDeleteBusy(true)
    setDeleteError('')
    try {
      await deleteSecret(deleteTarget.id)
      toast(t('secrets.delete.success'), 'success')
      const wasSelected = deleteTarget.id === value
      setDeleteTarget(null)
      await reload()
      // Deleting the selected secret clears the selection — the parent then
      // shows its own "secret missing"/empty state.
      if (wasSelected) onChange('')
    } catch (e) {
      setDeleteError(e instanceof Error ? e.message : String(e))
    } finally {
      setDeleteBusy(false)
    }
  }

  const inputCls = 'w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary disabled:opacity-50'

  return (
    <>
      <ModalField label={t('secretPicker.secret')}>
        <div ref={rootRef} className="relative" onKeyDown={onKeyDown}>
          <button
            ref={triggerRef}
            type="button"
            className="flex w-full items-center rounded border border-border bg-background px-2.5 py-1.5 text-left text-sm focus:outline-none focus:border-primary disabled:opacity-50"
            disabled={disabled}
            onClick={() => {
              if (!open) openPopup()
              setOpen((o) => !o)
              const selIdx = secrets.findIndex((s) => s.id === value)
              setActiveIdx(selIdx >= 0 ? selIdx : 0)
            }}
          >
            <span className="flex-1 truncate">
              {selected ? (selected.name || selected.id) : missing ? (
                <span className="text-destructive">{value} {t('secretPicker.notFound')}</span>
              ) : (
                <span className="text-muted-foreground">{t('secretPicker.placeholder')}</span>
              )}
            </span>
            <ChevronDown size={16} className="text-muted-foreground" />
          </button>

          {open && popupPos && createPortal(
            <div
              ref={popupRef}
              className="fixed z-50 overflow-y-auto rounded border border-border bg-card shadow-lg"
              style={{
                left: popupPos.left,
                width: popupPos.width,
                top: popupPos.top,
                bottom: popupPos.bottom,
                maxHeight: ITEM_HEIGHT * MAX_VISIBLE_ITEMS,
              }}
              role="listbox"
            >
              {visible.map((s, idx) => {
                const isSelected = s.id === value
                return (
                  <div
                    key={s.id}
                    role="option"
                    aria-selected={isSelected}
                    className={`group flex items-center hover:bg-muted ${idx === activeIdx ? 'bg-muted' : ''}`}
                    onMouseEnter={() => setActiveIdx(idx)}
                  >
                    <button
                      type="button"
                      tabIndex={-1}
                      title={s.id}
                      className={`min-w-0 flex-1 truncate px-2.5 py-1.5 text-left text-sm ${isSelected ? 'text-primary font-medium' : ''}`}
                      onClick={() => pick(s.id)}
                    >
                      {s.name || s.id}
                    </button>
                    <div className="flex shrink-0 gap-0.5 pr-1.5 opacity-0 group-hover:opacity-100">
                      <button
                        type="button"
                        tabIndex={-1}
                        aria-label={t('secrets.edit.tooltip')}
                        title={t('secrets.edit.tooltip')}
                        className="rounded p-1 text-muted-foreground hover:bg-background/60 hover:text-foreground"
                        onClick={(e) => { e.stopPropagation(); requestEdit(s) }}
                      >
                        <Pencil size={11} />
                      </button>
                      <button
                        type="button"
                        tabIndex={-1}
                        aria-label={t('common.delete')}
                        title={t('common.delete')}
                        className="rounded p-1 text-muted-foreground hover:bg-background/60 hover:text-destructive"
                        onClick={(e) => { e.stopPropagation(); requestDelete(s) }}
                      >
                        <Trash2 size={11} />
                      </button>
                    </div>
                  </div>
                )
              })}
              <div className={visible.length > 0 ? 'border-t border-border' : ''}>
                <button
                  type="button"
                  tabIndex={-1}
                  className={`flex w-full items-center gap-1.5 px-2.5 py-1.5 text-left text-sm hover:bg-muted ${activeIdx === visible.length ? 'bg-muted' : ''}`}
                  onMouseEnter={() => setActiveIdx(visible.length)}
                  onClick={openCreate}
                >
                  <Plus size={12} />
                  {t('secretPicker.addNew')}
                </button>
              </div>
              {/* Host filter escape: reveal secrets bound to other hosts so one
                  can be reused here (creds differ per host, but the choice is
                  the user's). */}
              {filteringByHost && hiddenCount > 0 && (
                <div className="border-t border-border">
                  <button
                    type="button"
                    tabIndex={-1}
                    className="flex w-full items-center px-2.5 py-1.5 text-left text-xs text-muted-foreground hover:bg-muted"
                    onClick={() => setShowAllHosts(true)}
                  >
                    {t('secretPicker.showAllHosts', { count: hiddenCount })}
                  </button>
                </div>
              )}
            </div>,
            portalEl ?? document.body,
          )}
        </div>
      </ModalField>

      {/* "Add new…" modal — Name (defaulted from the repo host when known) +
          write-only Token. Cancel leaves the dropdown on its previous value
          (the selection only changes after a successful create). */}
      <Modal
        open={createOpen}
        title={isRegistry ? t('secretPicker.createTitleRegistry') : t('secretPicker.createTitle')}
        onClose={() => setCreateOpen(false)}
        onSubmit={handleCreate}
        footer={
          <>
            <button
              type="button"
              className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50"
              onClick={() => setCreateOpen(false)}
              disabled={createBusy}
            >
              {t('common.cancel')}
            </button>
            <button
              type="submit"
              disabled={createBusy}
              className="rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-xs font-medium hover:bg-primary/90 disabled:opacity-50"
            >
              {createBusy ? <Loader2 size={12} className="animate-spin" /> : t('common.create')}
            </button>
          </>
        }
      >
        {isRegistry && host && (
          <ModalField label={t('secretPicker.host')}>
            <input type="text" value={host} className={inputCls} disabled readOnly />
          </ModalField>
        )}
        <ModalField label={t('secretPicker.name')} required>
          <input
            type="text"
            autoFocus
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            className={inputCls}
            disabled={createBusy}
          />
        </ModalField>
        {isRegistry && (
          <ModalField label={t('secretPicker.username')} required>
            <input
              type="text"
              value={newUser}
              onChange={(e) => setNewUser(e.target.value)}
              className={inputCls}
              disabled={createBusy}
              autoComplete="username"
            />
          </ModalField>
        )}
        <ModalField label={isRegistry ? t('secretPicker.password') : t('secretPicker.token')} required>
          <input
            type="password"
            value={newToken}
            onChange={(e) => setNewToken(e.target.value)}
            className={inputCls}
            disabled={createBusy}
            autoComplete="new-password"
          />
        </ModalField>
        {createError && (
          <div className="rounded-md bg-destructive/10 border border-destructive/20 px-3 py-2 text-xs text-destructive">
            {createError}
          </div>
        )}
      </Modal>

      {/* Internal write-only edit dialog (only when the parent doesn't
          delegate via onEditRequest). */}
      {!onEditRequest && (
        <SecretEditDialog
          open={!!editTarget}
          secret={editTarget}
          onClose={() => setEditTarget(null)}
          onSaved={() => {
            setEditTarget(null)
            void reload()
          }}
        />
      )}

      <ConfirmModal
        open={!!deleteTarget}
        title={t('secrets.delete.title')}
        message={secretDeleteMessage(
          t,
          deleteTarget?.name || deleteTarget?.id || '',
          workspacesUsingSecret(deleteTarget?.id ?? '', workspaces),
        )}
        confirmLabel={t('common.delete')}
        confirmVariant="danger"
        loading={deleteBusy}
        error={deleteError}
        onConfirm={handleDelete}
        onCancel={() => { setDeleteTarget(null); setDeleteError('') }}
      />
    </>
  )
}
