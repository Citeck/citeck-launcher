import { useEffect, useState, useCallback } from 'react'
import { Trash2, FlaskConical } from 'lucide-react'
import { getSecrets, createSecret, deleteSecret, testSecret } from '../lib/api'
import type { SecretMetaDto, SecretCreateDto } from '../lib/types'
import { JournalDialog, type JournalAction, type JournalColumn } from './JournalDialog'
import { ConfirmModal } from './ConfirmModal'
import { FormDialog, type FormFieldSpec } from './FormDialog'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'

interface SecretRow extends Record<string, unknown> {
  id: string
  name: string
  type: string
  scope: string
  created: string
}

interface SecretsDialogProps {
  open: boolean
  onClose: () => void
}

// Kotlin parity (AuthType.kt:3-7): displayed names mirror the JVM launcher's
// AuthType.displayName — "Token" / "Basic (Username/Password)". REGISTRY_AUTH
// is Go-only (the JVM launcher had no separate registry auth secret), so it
// reuses the Basic display name with a registry suffix to disambiguate.
function buildSecretTypes(t: (key: string, params?: Record<string, string | number>) => string) {
  return [
    { label: t('secrets.type.gitToken'), value: 'GIT_TOKEN' },
    { label: t('secrets.type.basicAuth'), value: 'BASIC_AUTH' },
    { label: t('secrets.type.registryAuth'), value: 'REGISTRY_AUTH' },
  ]
}

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

  const reload = useCallback(() => {
    getSecrets()
      .then((s) => setSecrets(s.map(toRow)))
      .catch((e) => toast((e as Error).message, 'error'))
  }, [])

  useEffect(() => {
    if (open) reload()
  }, [open, reload])

  const columns: JournalColumn<SecretRow>[] = [
    { label: t('secrets.table.name'), key: 'name', width: '40%' },
    { label: t('secrets.table.type'), key: 'type', width: '25%' },
    { label: t('secrets.table.scope'), key: 'scope' },
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
      icon: Trash2,
      title: t('common.delete'),
      variant: 'danger',
      onClick: (row) => setDeleteTarget(row),
    },
  ]

  async function handleCreate(values: Record<string, unknown>) {
    setCreating(true)
    setCreateError(null)
    try {
      const type = String(values.type || 'GIT_TOKEN')
      const payload: SecretCreateDto = {
        id: String(values.id || '').trim(),
        name: String(values.name || '').trim(),
        type,
        value: String(values.value || ''),
      }
      if (type === 'BASIC_AUTH' || type === 'REGISTRY_AUTH') {
        payload.username = String(values.username || '').trim()
      }
      await createSecret(payload)
      toast(t('secrets.create.success'), 'success')
      setCreateOpen(false)
      reload()
    } catch (e) {
      setCreateError((e as Error).message)
    } finally {
      setCreating(false)
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

  const isBasicLike = (ctx: Record<string, unknown>) =>
    ctx.type === 'BASIC_AUTH' || ctx.type === 'REGISTRY_AUTH'

  const createFields: FormFieldSpec[] = [
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
      type: 'password',
      required: true,
      placeholder: t('secrets.form.value.placeholder'),
    },
  ]

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
      <ConfirmModal
        open={!!deleteTarget}
        title={t('secrets.delete.title')}
        message={t('secrets.delete.message', { name: deleteTarget?.name ?? '' })}
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
    scope: s.scope ?? '',
    created: s.createdAt ? new Date(s.createdAt).toLocaleString() : '',
  }
}
