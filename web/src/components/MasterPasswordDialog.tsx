import { useEffect, useRef, useState } from 'react'
import { Eye, EyeOff } from 'lucide-react'
import { ConfirmModal } from './ConfirmModal'
import { useTranslation } from '../lib/i18n'

/**
 * MasterPasswordDialog covers the three Kotlin master-password flows:
 *
 *  - "kotlin-decrypt" (one-time migration): user types the master password
 *    that originally encrypted the Kotlin H2 store; daemon decrypts +
 *    re-encrypts under the new envelope.
 *  - "create" (CreateMasterPwdDialog.kt): user creates a brand-new master
 *    password. Two fields with "passwords must match" validation; Enter
 *    in either submits.
 *  - "ask" (AskMasterPasswordDialog.kt): user unlocks an existing envelope.
 *    Adds a "Reset Master Password and Drop All Secrets" button gated by
 *    a nested confirm dialog.
 *
 * The dialog is presentational; the parent owns network + state.
 */
export type MasterPasswordMode = 'kotlin-decrypt' | 'create' | 'ask'

interface MasterPasswordDialogProps {
  mode: MasterPasswordMode
  open: boolean
  loading: boolean
  error: string | null
  /** Called with the entered password when user confirms. */
  onSubmit: (password: string) => void | Promise<void>
  /** "Skip" button — only shown for kotlin-decrypt + ask modes. Pass undefined to hide. */
  onSkip?: () => void
  /** "Reset Master Password and Drop All Secrets" — only meaningful in ask mode. */
  onReset?: () => void | Promise<void>
}

export function MasterPasswordDialog({
  mode,
  open,
  loading,
  error,
  onSubmit,
  onSkip,
  onReset,
}: MasterPasswordDialogProps) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    else if (!open && dialog.open) dialog.close()
  }, [open])

  return (
    <dialog
      ref={dialogRef}
      className="fixed inset-0 z-50 m-auto max-w-md w-full rounded-lg border border-border bg-card p-0 text-foreground backdrop:bg-black/50"
      // No onClose: dialog is dismiss-only via explicit buttons (Kotlin parity).
    >
      {/* key resets form state on each open/close toggle — avoids setState-in-effect */}
      <MasterPasswordForm
        key={open ? 1 : 0}
        mode={mode}
        loading={loading}
        error={error}
        onSubmit={onSubmit}
        onSkip={onSkip}
        onReset={onReset}
      />
    </dialog>
  )
}

interface MasterPasswordFormProps {
  mode: MasterPasswordMode
  loading: boolean
  error: string | null
  onSubmit: (password: string) => void | Promise<void>
  onSkip?: () => void
  onReset?: () => void | Promise<void>
}

function MasterPasswordForm({ mode, loading, error, onSubmit, onSkip, onReset }: MasterPasswordFormProps) {
  const { t } = useTranslation()
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  // Two reveal flags so the confirm field toggles independently of the primary
  // password field (each eye icon controls only its own input).
  const [revealed, setRevealed] = useState(false)
  const [revealedConfirm, setRevealedConfirm] = useState(false)
  const [confirmReset, setConfirmReset] = useState(false)
  const [localError, setLocalError] = useState<string | null>(null)

  function submit() {
    setLocalError(null)
    if (!password) {
      setLocalError(t('migration.password.empty'))
      return
    }
    if (mode === 'create' && password !== confirmPassword) {
      setLocalError(t('migration.password.mismatch'))
      return
    }
    void onSubmit(password)
  }

  const showError = error ?? localError

  return (
    <>
      <div className="p-6">
        <h2 className="text-lg font-semibold mb-2">
          {mode === 'kotlin-decrypt' && t('migration.title')}
          {mode === 'create' && t('migration.setupPassword.title')}
          {mode === 'ask' && t('migration.unlock.title')}
        </h2>
        <p className="text-sm text-muted-foreground mb-4">
          {mode === 'kotlin-decrypt' && t('migration.description')}
          {mode === 'create' && t('migration.setupPassword.description')}
          {mode === 'ask' && t('migration.unlock.description')}
        </p>

        <PasswordField
          value={password}
          onChange={setPassword}
          revealed={revealed}
          onToggleReveal={() => setRevealed((r) => !r)}
          onEnter={submit}
          autoFocus
        />

        {mode === 'create' && (
          <div className="mt-2">
            <PasswordField
              value={confirmPassword}
              onChange={setConfirmPassword}
              revealed={revealedConfirm}
              onToggleReveal={() => setRevealedConfirm((r) => !r)}
              onEnter={submit}
              placeholder={t('migration.password.confirmPlaceholder')}
            />
          </div>
        )}

        {showError && <p className="text-destructive text-sm mt-2">{showError}</p>}

        <div className="mt-4 flex items-center justify-between gap-2 flex-wrap">
          {mode === 'ask' && onReset && (
            <button
              type="button"
              className="text-xs text-destructive/80 hover:text-destructive"
              onClick={() => setConfirmReset(true)}
              disabled={loading}
            >
              {t('migration.unlock.reset')}
            </button>
          )}
          <div className="flex items-center gap-2 ml-auto">
            {onSkip && (mode === 'kotlin-decrypt' || mode === 'ask') && (
              <button
                type="button"
                className="text-sm text-muted-foreground hover:text-foreground"
                onClick={onSkip}
                disabled={loading}
              >
                {t('migration.skip')}
              </button>
            )}
            <button
              type="button"
              className="rounded bg-primary text-primary-foreground px-4 py-1.5 text-sm font-medium disabled:opacity-50"
              onClick={submit}
              disabled={loading || !password || (mode === 'create' && !confirmPassword)}
            >
              {loading ? '…' : mode === 'ask' ? t('migration.unlock.confirm') : t('migration.confirm')}
            </button>
          </div>
        </div>
      </div>

      <ConfirmModal
        open={confirmReset}
        title={t('migration.unlock.reset.confirmTitle')}
        message={t('migration.unlock.reset.confirmMessage')}
        confirmLabel={t('migration.unlock.reset.confirmLabel')}
        confirmVariant="danger"
        onConfirm={async () => {
          if (onReset) await onReset()
          setConfirmReset(false)
        }}
        onCancel={() => setConfirmReset(false)}
      />
    </>
  )
}

interface PasswordFieldProps {
  value: string
  onChange: (v: string) => void
  revealed: boolean
  onToggleReveal: () => void
  onEnter: () => void
  placeholder?: string
  autoFocus?: boolean
}

function PasswordField({ value, onChange, revealed, onToggleReveal, onEnter, placeholder, autoFocus }: PasswordFieldProps) {
  return (
    <div className="relative">
      <input
        type={revealed ? 'text' : 'password'}
        className="w-full px-3 py-2 pr-10 bg-background border border-border rounded text-foreground"
        placeholder={placeholder}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => { if (e.key === 'Enter') onEnter() }}
        autoFocus={autoFocus}
      />
      <button
        type="button"
        className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
        onClick={onToggleReveal}
        tabIndex={-1}
      >
        {revealed ? <EyeOff size={16} /> : <Eye size={16} />}
      </button>
    </div>
  )
}
