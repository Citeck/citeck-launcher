import { useCallback, useEffect, useState } from 'react'
import { MasterPasswordDialog } from './MasterPasswordDialog'
import {
  getMigrationStatus,
  submitMasterPassword,
  unlockSecrets,
  resetSecrets,
} from '../lib/api'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'
import { useDashboardStore } from '../lib/store'

type Step = 'kotlin-decrypt' | 'unlock' | null

/**
 * Mounted at the App layout level. Handles two startup-time scenarios:
 *
 *  - `kotlin-decrypt`: an encrypted secret blob carried over from the Kotlin
 *    1.x launcher needs the user's old master password to be imported.
 *  - `unlock`: the user already set a custom master password in a previous
 *    session and the SecretService is locked until they enter it.
 *
 * The `setup-password` step is intentionally NOT handled here — Kotlin v1.x
 * never asked for a master password until the user actually created their
 * first user secret. We match that contract by deferring CreateMasterPwd to
 * `SecretsDialog`, which catches the daemon's `ENCRYPTION_NOT_SET_UP` error
 * on save and runs the dialog inline.
 */
export function SecretsUnlockGuard() {
  const { t } = useTranslation()
  const fetchData = useDashboardStore((s) => s.fetchData)

  const [step, setStep] = useState<Step>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [checked, setChecked] = useState(false)

  useEffect(() => {
    if (checked || step) return
    // Intentional: one-shot guard flag for the on-mount migration-status check;
    // the `checked` guard makes this run exactly once, not a cascading render.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setChecked(true)
    getMigrationStatus().then((s) => {
      if (s.hasPendingSecrets) setStep('kotlin-decrypt')
      else if (s.encrypted && s.locked) setStep('unlock')
    }).catch(() => { /* silent — daemon down handled elsewhere */ })
  }, [checked, step])

  const handleKotlinDecrypt = useCallback(async (pwd: string) => {
    if (!pwd) return
    setLoading(true)
    setError('')
    try {
      await submitMasterPassword(pwd)
      toast(t('migration.secretsImported'), 'success')
      setStep(null)
      fetchData()
    } catch {
      setError(t('migration.wrongPassword'))
    } finally {
      setLoading(false)
    }
  }, [fetchData, t])

  const handleUnlock = useCallback(async (pwd: string) => {
    if (!pwd) return
    setLoading(true)
    setError('')
    try {
      await unlockSecrets(pwd)
      toast(t('migration.unlock.success'), 'success')
      setStep(null)
      fetchData()
    } catch {
      setError(t('migration.wrongPassword'))
    } finally {
      setLoading(false)
    }
  }, [fetchData, t])

  const handleSkip = useCallback(() => {
    setStep(null)
    setError('')
  }, [])

  const handleReset = useCallback(async () => {
    setLoading(true)
    try {
      await resetSecrets()
      toast(t('migration.unlock.reset.success'), 'success')
      setStep(null)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [t])

  return (
    <MasterPasswordDialog
      mode={step === 'unlock' ? 'ask' : 'kotlin-decrypt'}
      open={!!step}
      loading={loading}
      error={error}
      onSubmit={async (pwd) => {
        if (step === 'kotlin-decrypt') await handleKotlinDecrypt(pwd)
        else if (step === 'unlock') await handleUnlock(pwd)
      }}
      onSkip={handleSkip}
      onReset={step === 'unlock' ? handleReset : undefined}
    />
  )
}
