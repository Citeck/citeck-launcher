import { useState } from 'react'
import { Loader2 } from 'lucide-react'
import { Modal, ModalField } from './Modal'
import { Select } from './Select'
import { SecretPicker } from './SecretPicker'
import { createWorkspace, updateWorkspace } from '../lib/api'
import type { WorkspaceCreateDto, WorkspaceDto, WorkspaceUpdateDto } from '../lib/types'
import { extractHost } from '../lib/giturl'
import { useTranslation } from '../lib/i18n'

/** 'create' = brand-new workspace; { kind: 'edit' } = mutate an existing one. */
export type FormMode = 'create' | { kind: 'edit'; ws: WorkspaceDto }

interface WorkspaceFormDialogProps {
  mode: FormMode
  onClose: () => void
  /** Called after a successful save. On create the freshly created workspace is
   *  passed so the parent can auto-activate it; on edit the arg is undefined. */
  onSaved: (createdWs?: WorkspaceDto) => void
}

/**
 * The TYPED workspace form — name + the git settings that actually matter
 * (repo URL, branch, pull period, auth type, token secret).
 *
 * Lives in its own module rather than inside WorkspaceSelector because it has
 * two callers now: the selector's dropdown/gear (Welcome screen, desktop) and
 * the Settings page, which is the only route that stays reachable once a
 * namespace is open. Users kept hunting for git settings on the namespace card
 * and landing in the raw workspace-v1.yml editor instead; both entry points
 * must lead here.
 */
export function WorkspaceFormDialog({ mode, onClose, onSaved }: WorkspaceFormDialogProps) {
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
        onSaved()
      } else {
        const create: WorkspaceCreateDto = {
          name: name.trim(),
          repoUrl: repoUrl.trim(),
          repoBranch: repoBranch.trim() || undefined,
          repoPullPeriod: repoPullPeriod.trim() || undefined,
          authType,
        }
        if (authType === 'TOKEN' && secretId) create.secretId = secretId
        const created = await createWorkspace(create)
        onSaved(created)
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const title = isEdit ? t('config.workspace.editTitle') : t('welcome.workspace.create')
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
