import { useEffect, useState, useCallback, useMemo } from 'react'
import { Trash2, FlaskConical, Pencil } from 'lucide-react'
import { ApiError, getSecrets, createSecret, deleteSecret, testSecret, setupSecretsPassword, listWorkspaces } from '../lib/api'
import type { SecretMetaDto, SecretCreateDto, WorkspaceDto } from '../lib/types'
import { JournalDialog, type JournalAction, type JournalColumn } from './JournalDialog'
import { ConfirmModal } from './ConfirmModal'
import { FormDialog, type FormFieldSpec } from './FormDialog'
import { SecretEditDialog } from './SecretEditDialog'
import { MasterPasswordDialog } from './MasterPasswordDialog'
import { formatDateTime } from '../lib/datetime'
import { useTranslation, type LocaleKey } from '../lib/i18n'
import { secretDeleteMessage, workspacesUsingSecret, secretValueLabelKey } from '../lib/secretPicker'
import { toast } from '../lib/toast'

interface SecretRow extends Record<string, unknown> {
  id: string
  name: string
  type: string
  username: string
  created: string
}

interface SecretsDialogProps {
  open: boolean
  onClose: () => void
}

// Kotlin parity (AuthType.kt:3-7): displayed names mirror the JVM launcher's
// AuthType.displayName — "Token" / "Basic (Username/Password)". REGISTRY_AUTH
// is intentionally NOT offered here: registry credentials are created through
// the dedicated host-scoped RegistryCredentialsDialog (which sets the
// "images-repo:<host>" scope the daemon matches on). A bare REGISTRY_AUTH made
// in this generic dialog binds to no registry and only confused users, so the
// two near-identical "Basic" entries collapse to one. Existing REGISTRY_AUTH
// secrets still list / edit / delete here.
function buildSecretTypes(t: (key: LocaleKey, params?: Record<string, string | number>) => string) {
  return [
    { label: t('secrets.type.gitToken'), value: 'GIT_TOKEN' },
    { label: t('secrets.type.basicAuth'), value: 'BASIC_AUTH' },
  ]
}

const isBasicLike = (ctx: Record<string, unknown>) =>
  ctx.type === 'BASIC_AUTH' || ctx.type === 'REGISTRY_AUTH'

/**
 * SecretsDialog is the modal port of Kotlin's "Show Auth Secrets" affordance
 * (NamespaceScreen.kt:380 — a JournalSelectDialog over AuthSecret entities).
 *
 * Single-source-of-truth for secret CRUD in the namespace screen, replacing
 * the previous /secrets route as the primary entry point. The route is kept
 * as a fallback so the URL still resolves for power users.
 */
export function SecretsDialog({ open, onClose }: SecretsDialogProps) {
  const { t } = useTranslation()
  const [secrets, setSecrets] = useState<SecretRow[]>([])
  const [deleteTarget, setDeleteTarget] = useState<SecretRow | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)
  // Write-only edit — delegated to the shared SecretEditDialog (the form
  // shows meta and an EMPTY value field; empty/absent fields keep their
  // stored values on the daemon).
  const [editTarget, setEditTarget] = useState<SecretRow | null>(null)
  // Workspaces referencing secrets via secretId (desktop only — [] in server
  // mode), used to warn before deleting a secret a workspace depends on.
  const [workspaces, setWorkspaces] = useState<WorkspaceDto[]>([])
  // When the daemon reports ENCRYPTION_NOT_SET_UP, we cache the pending save
  // payload, run CreateMasterPwd, then retry the save with the same data so
  // the user doesn't have to retype.
  const [pendingPayload, setPendingPayload] = useState<SecretCreateDto | null>(null)
  const [createMasterOpen, setCreateMasterOpen] = useState(false)
  const [createMasterLoading, setCreateMasterLoading] = useState(false)
  const [createMasterError, setCreateMasterError] = useState('')
  const [loading, setLoading] = useState(false)

  const reload = useCallback(() => {
    void Promise.resolve().then(() => {
      setLoading(true)
      // Best-effort: workspace secretId references power the delete warning;
      // any failure (older daemon, server mode) degrades to no warning.
      void listWorkspaces().catch(() => [] as WorkspaceDto[]).then(setWorkspaces)
      return getSecrets()
        // SYSTEM secrets (_jwt, _oidc, _citeck_sa) are daemon-managed and have
        // sensible defaults — the user shouldn't see them in the Secrets UI
        // (they neither edit them nor reason about them). Only user-added
        // GIT_TOKEN / BASIC_AUTH / REGISTRY_AUTH entries belong here.
        .then((s) => setSecrets(s.filter((sec) => sec.type !== 'SYSTEM').map(toRow)))
        .catch((e) => toast((e as Error).message, 'error'))
        .finally(() => setLoading(false))
    })
  }, [])

  useEffect(() => {
    if (open) reload()
  }, [open, reload])

  const columns: JournalColumn<SecretRow>[] = [
    { label: t('secrets.table.name'), key: 'name', width: '60%' },
    { label: t('secrets.table.type'), key: 'type' },
  ]

  const rowActions: JournalAction<SecretRow>[] = [
    {
      icon: FlaskConical,
      title: t('secrets.test.tooltip'),
      onClick: async (row) => {
        try {
          await testSecret(row.id)
          toast(t('secrets.test.ok'), 'success')
        } catch (e) {
          toast((e as Error).message, 'error')
        }
      },
    },
    {
      icon: Pencil,
      title: t('secrets.edit.tooltip'),
      onClick: (row) => setEditTarget(row),
    },
    {
      icon: Trash2,
      title: t('common.delete'),
      variant: 'danger',
      onClick: (row) => setDeleteTarget(row),
    },
  ]

  async function savePayload(payload: SecretCreateDto): Promise<boolean> {
    await createSecret(payload)
    toast(t('secrets.create.success'), 'success')
    setCreateOpen(false)
    setPendingPayload(null)
    reload()
    return true
  }

  async function handleCreate(values: Record<string, unknown>) {
    setCreating(true)
    setCreateError(null)
    try {
      const type = String(values.type || 'GIT_TOKEN')
      const id = String(values.id || '').trim()
      const payload: SecretCreateDto = {
        id,
        name: String(values.name || '').trim(),
        type,
        value: String(values.value || ''),
      }
      // Secrets created here are global. Registry credentials are managed
      // through the dedicated RegistryCredentialsDialog (which sets the
      // "images-repo:<host>" scope the daemon matches on); a power user who
      // hand-types that id convention still gets it bound to the registry.
      if (type === 'REGISTRY_AUTH' && id.startsWith('images-repo:')) {
        payload.scope = id
      }
      if (type === 'BASIC_AUTH' || type === 'REGISTRY_AUTH') {
        payload.username = String(values.username || '').trim()
      }
      try {
        await savePayload(payload)
      } catch (e) {
        if (e instanceof ApiError && e.code === 'ENCRYPTION_NOT_SET_UP') {
          // Desktop first-time secret. Stash the payload, open CreateMasterPwd;
          // after the user picks a master, we'll retry the save.
          setPendingPayload(payload)
          setCreateError(null)
          setCreateMasterOpen(true)
          return
        }
        throw e
      }
    } catch (e) {
      setCreateError((e as Error).message)
    } finally {
      setCreating(false)
    }
  }

  async function handleCreateMaster(pwd: string) {
    if (!pwd) return
    setCreateMasterLoading(true)
    setCreateMasterError('')
    try {
      await setupSecretsPassword(pwd)
      setCreateMasterOpen(false)
      if (pendingPayload) {
        setCreating(true)
        try {
          await savePayload(pendingPayload)
        } catch (e) {
          setCreateError((e as Error).message)
        } finally {
          setCreating(false)
        }
      }
    } catch (e) {
      setCreateMasterError((e as Error).message)
    } finally {
      setCreateMasterLoading(false)
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return
    try {
      await deleteSecret(deleteTarget.id)
      toast(t('secrets.delete.success'), 'success')
      setDeleteTarget(null)
      reload()
    } catch (e) {
      toast((e as Error).message, 'error')
    }
  }

  // Memoized so the spec keeps a stable identity across re-renders (e.g. the
  // `creating` flag flipping during submit) — FormDialog resets its values
  // whenever the fields prop identity changes, which would wipe the user's
  // input on a failed save.
  const createFields: FormFieldSpec[] = useMemo(() => [
    { key: 'id', label: t('secrets.form.id'), type: 'text', required: true, placeholder: t('secrets.form.id.placeholder') },
    { key: 'name', label: t('secrets.form.name'), type: 'text', required: true, placeholder: t('secrets.form.name.placeholder') },
    {
      key: 'type',
      label: t('secrets.form.type'),
      type: 'select',
      defaultValue: 'GIT_TOKEN',
      options: buildSecretTypes(t),
    },
    {
      key: 'username',
      label: t('secrets.form.username'),
      type: 'text',
      placeholder: t('secrets.form.username.placeholder'),
      visibleWhen: isBasicLike,
      dependsOn: ['type'],
      validations: [
        (ctx, value) => {
          if (!isBasicLike(ctx)) return ''
          const s = (value as string | undefined)?.trim() ?? ''
          return s ? '' : t('form.fieldRequired', { label: t('secrets.form.username') })
        },
      ],
    },
    {
      key: 'value',
      label: t('secrets.form.value'),
      // "Token" for a GIT_TOKEN, "Password" for Basic — "Value" was too vague.
      labelWhen: (ctx) => t(secretValueLabelKey(String(ctx.type || 'GIT_TOKEN'))),
      dependsOn: ['type'],
      type: 'password',
      required: true,
      placeholder: t('secrets.form.value.placeholder'),
    },
  ], [t])

  return (
    <>
      <JournalDialog<SecretRow>
        open={open}
        onClose={onClose}
        title={t('secrets.dialog.title')}
        columns={columns}
        data={secrets}
        rowActions={rowActions}
        onCreate={() => setCreateOpen(true)}
        loading={loading}
        hideSearch
      />
      <FormDialog
        open={createOpen}
        title={t('secrets.add')}
        fields={createFields}
        onSubmit={handleCreate}
        onCancel={() => { setCreateOpen(false); setCreateError(null) }}
        loading={creating}
        error={createError}
        submitLabel={t('common.create')}
      />
      <SecretEditDialog
        open={!!editTarget}
        secret={editTarget}
        onClose={() => setEditTarget(null)}
        onSaved={() => {
          setEditTarget(null)
          reload()
        }}
      />
      {/* Master-password setup runs only when the user actually attempts to
          save a user secret on a fresh desktop install (Kotlin parity —
          CreateMasterPwdDialog from view/commons/dialog). Cancelling leaves
          the pending payload in memory so the user can choose to abort the
          whole create flow. */}
      <MasterPasswordDialog
        mode="create"
        open={createMasterOpen}
        loading={createMasterLoading}
        error={createMasterError}
        onSubmit={handleCreateMaster}
        onSkip={() => {
          setCreateMasterOpen(false)
          setPendingPayload(null)
        }}
      />
      <ConfirmModal
        open={!!deleteTarget}
        title={t('secrets.delete.title')}
        message={deleteMessage(t, deleteTarget, workspaces)}
        confirmLabel={t('common.delete')}
        confirmVariant="danger"
        onConfirm={handleDelete}
        onCancel={() => setDeleteTarget(null)}
      />
    </>
  )
}

function toRow(s: SecretMetaDto): SecretRow {
  return {
    id: s.id,
    name: s.name,
    type: s.type,
    username: s.username ?? '',
    created: s.createdAt ? formatDateTime(s.createdAt) : '',
  }
}

// Delete-confirm text; appends a warning when any workspace references the
// secret via secretId (those workspaces would lose repo access). Shared
// helpers in lib/secretPicker.ts keep this in sync with the picker's delete.
function deleteMessage(
  t: (key: LocaleKey, params?: Record<string, string | number>) => string,
  target: SecretRow | null,
  workspaces: WorkspaceDto[],
): string {
  const usedBy = target ? workspacesUsingSecret(target.id, workspaces) : []
  return secretDeleteMessage(t, target?.name ?? '', usedBy)
}
