import { useMemo, useState } from 'react'
import { updateSecret } from '../lib/api'
import type { SecretUpdateDto } from '../lib/types'
import { FormDialog, type FormFieldSpec, type SelectOption } from './FormDialog'
import { CUSTOM_SCOPE } from '../lib/secretPicker'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'

/** Minimal meta of the secret being edited (the value itself is never read). */
export interface SecretEditTarget {
  id: string
  name: string
  type: string
  scope?: string
  username?: string
}

interface SecretEditDialogProps {
  open: boolean
  secret: SecretEditTarget | null
  /** Richer scope select options (SecretsDialog passes its derived list);
   *  defaults to Global + the secret's current scope + Custom…. */
  scopeOptions?: SelectOption[]
  onClose: () => void
  /** Called after a successful save — close + refresh on the caller's side. */
  onSaved: () => void
}

/**
 * Shared write-only secret edit modal, extracted from SecretsDialog so the
 * SecretPicker dropdown rows and GitPullErrorDialog's "Edit token" action
 * reuse the exact same flow: the form shows meta (name/scope/username) and
 * an EMPTY value field — empty/absent fields keep their stored values on the
 * daemon. Typing a new value is the fix path for a bad/expired token.
 */
export function SecretEditDialog({ open, secret, scopeOptions, onClose, onSaved }: SecretEditDialogProps) {
  const { t } = useTranslation()
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Default scope options when the caller has no richer list: "Global", the
  // secret's current scope (so the prefill matches an actual option), and the
  // "Custom…" free-text escape. 'global' is the store's marker for unscoped.
  const scopeOpts = useMemo<SelectOption[]>(() => {
    if (scopeOptions) return scopeOptions
    const current = secret?.scope && secret.scope !== 'global' ? secret.scope : ''
    return [
      { label: t('secrets.form.scope.global'), value: '' },
      ...(current ? [{ label: current, value: current }] : []),
      { label: t('secrets.form.scope.custom'), value: CUSTOM_SCOPE },
    ]
  }, [scopeOptions, secret, t])

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
      {
        key: 'scope',
        label: t('secrets.form.scope'),
        type: 'select',
        defaultValue: '',
        options: scopeOpts,
      },
      {
        key: 'scopeCustom',
        label: t('secrets.form.scope.customValue'),
        type: 'text',
        placeholder: t('secrets.form.scope.placeholder'),
        visibleWhen: (ctx) => ctx.scope === CUSTOM_SCOPE,
        dependsOn: ['scope'],
      },
    ]
  }, [t, scopeOpts, secret])

  // 'global' is the store's marker for "unscoped" — map it to the Global ('')
  // select option so the prefill matches an actual option.
  const initialValues = useMemo(() => secret ? {
    name: secret.name,
    username: secret.username ?? '',
    value: '',
    scope: secret.scope === 'global' ? '' : (secret.scope ?? ''),
  } : undefined, [secret])

  // Write-only partial update: only non-empty fields are sent; the daemon
  // keeps stored values for everything absent — in particular the secret
  // VALUE when the password field is left empty.
  async function handleSubmit(values: Record<string, unknown>) {
    if (!secret) return
    setSaving(true)
    setError(null)
    try {
      let scope = String(values.scope || '').trim()
      if (scope === CUSTOM_SCOPE) scope = String(values.scopeCustom || '').trim()
      const data: SecretUpdateDto = {}
      const name = String(values.name || '').trim()
      if (name) data.name = name
      if (scope) data.scope = scope
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
