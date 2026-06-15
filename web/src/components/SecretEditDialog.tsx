import { useMemo, useState } from 'react'
import { updateSecret } from '../lib/api'
import type { SecretUpdateDto } from '../lib/types'
import { FormDialog, type FormFieldSpec } from './FormDialog'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'

/** Minimal meta of the secret being edited (the value itself is never read). */
export interface SecretEditTarget {
  id: string
  name: string
  type: string
  username?: string
}

interface SecretEditDialogProps {
  open: boolean
  secret: SecretEditTarget | null
  onClose: () => void
  /** Called after a successful save — close + refresh on the caller's side. */
  onSaved: () => void
}

/**
 * Shared write-only secret edit modal, extracted from SecretsDialog so the
 * SecretPicker dropdown rows and GitPullErrorDialog's "Edit token" action
 * reuse the exact same flow: the form shows meta (name/username) and an EMPTY
 * value field — empty/absent fields keep their stored values on the daemon.
 * Typing a new value is the fix path for a bad/expired token. The stored
 * scope (e.g. a registry secret's "images-repo:<host>") is left untouched —
 * the partial update omits it, so the daemon preserves it.
 */
export function SecretEditDialog({ open, secret, onClose, onSaved }: SecretEditDialogProps) {
  const { t } = useTranslation()
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Memoized so the spec keeps a stable identity across re-renders — see
  // FormDialog: it resets its values whenever the fields prop identity
  // changes, which would wipe the user's input on a failed save.
  const fields: FormFieldSpec[] = useMemo(() => {
    if (!secret) return []
    return [
      { key: 'name', label: t('secrets.form.name'), type: 'text', required: true, placeholder: t('secrets.form.name.placeholder') },
      {
        key: 'username',
        label: t('secrets.form.username'),
        type: 'text',
        placeholder: t('secrets.form.username.placeholder'),
        visible: secret.type === 'BASIC_AUTH' || secret.type === 'REGISTRY_AUTH',
      },
      {
        key: 'value',
        label: t('secrets.form.value'),
        type: 'password',
        placeholder: t('secrets.form.value.keepPlaceholder'),
      },
    ]
  }, [t, secret])

  const initialValues = useMemo(() => secret ? {
    name: secret.name,
    username: secret.username ?? '',
    value: '',
  } : undefined, [secret])

  // Write-only partial update: only non-empty fields are sent; the daemon
  // keeps stored values for everything absent — in particular the secret
  // VALUE when the password field is left empty.
  async function handleSubmit(values: Record<string, unknown>) {
    if (!secret) return
    setSaving(true)
    setError(null)
    try {
      const data: SecretUpdateDto = {}
      const name = String(values.name || '').trim()
      if (name) data.name = name
      const username = String(values.username || '').trim()
      if (username) data.username = username
      const value = String(values.value || '')
      if (value) data.value = value
      await updateSecret(secret.id, data)
      toast(t('secrets.update.success'), 'success')
      onSaved()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open={open}
      title={t('secrets.edit.title')}
      fields={fields}
      initialValues={initialValues}
      onSubmit={handleSubmit}
      onCancel={() => { setError(null); onClose() }}
      loading={saving}
      error={error}
      submitLabel={t('common.save')}
    />
  )
}
