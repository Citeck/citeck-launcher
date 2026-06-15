import { useState } from 'react'
import { createSecret, postAppRestart } from '../lib/api'
import { FormDialog, type FormFieldSpec } from './FormDialog'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'

interface RegistryCredentialsDialogProps {
  open: boolean
  /** Docker registry hostname (the part before the first '/' in the image URL). */
  host: string
  /** Optional app to restart after creds are saved (so pull retries immediately). */
  retryApp?: string
  /** Called only on successful save (not on cancel). Invoked AFTER onClose so
   * callers can fire a namespace-wide retry-pull-failed without parsing the
   * dialog's internal state. */
  onSaved?: () => void
  onClose: () => void
}

/**
 * Port of Kotlin's registry-credentials prompt (`AppImagePullAction.kt:155-178`).
 * Fires when a Docker pull returns 401/403 and the user needs to provide
 * BASIC auth credentials for the registry.
 *
 * On save:
 *  1. Creates a secret with id AND scope `images-repo:{host}`, type
 *     `REGISTRY_AUTH`. The daemon resolves registry credentials by SCOPE only
 *     (see resolveRegistryAuth in internal/daemon/server.go: it matches
 *     `scope == "images-repo:<host>"`; an empty scope defaults to "global" in
 *     the store and never matches). Username and password are sent as
 *     separate fields (SecretCreateDto.username / .value) so passwords
 *     containing ':' round-trip untouched.
 *  2. Optionally restarts `retryApp` so the next pull attempt picks up the
 *     newly-stored secret without waiting for the reconciler tick.
 */
export function RegistryCredentialsDialog({ open, host, retryApp, onSaved, onClose }: RegistryCredentialsDialogProps) {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const fields: FormFieldSpec[] = [
    {
      key: 'host',
      label: t('registryCreds.host'),
      type: 'display',
      defaultValue: host,
    },
    {
      key: 'username',
      label: t('registryCreds.username'),
      type: 'text',
      required: true,
    },
    {
      key: 'password',
      label: t('registryCreds.password'),
      type: 'password',
      required: true,
    },
  ]

  async function handleSubmit(values: Record<string, unknown>) {
    setLoading(true)
    setError(null)
    try {
      const username = String(values.username || '').trim()
      const password = String(values.password || '')
      // Daemon-side convention for registry auth secrets: the lookup key is
      // the SCOPE `images-repo:<host>` (the id is set to the same value for
      // readability, but only scope is matched). The Docker pull worker
      // resolves this on every retry, so saving the secret is sufficient to
      // unblock a stuck PULL_FAILED app.
      await createSecret({
        id: `images-repo:${host}`,
        name: `Registry ${host}`,
        type: 'REGISTRY_AUTH',
        scope: `images-repo:${host}`,
        username,
        value: password,
      })
      toast(t('registryCreds.saved'), 'success')
      if (retryApp) {
        try { await postAppRestart(retryApp) } catch { /* user can retry manually */ }
      }
      onClose()
      onSaved?.()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <FormDialog
      open={open}
      title={t('registryCreds.title', { host })}
      fields={fields}
      loading={loading}
      error={error}
      submitLabel={t('registryCreds.save')}
      onSubmit={handleSubmit}
      onCancel={() => { setError(null); onClose() }}
    />
  )
}
