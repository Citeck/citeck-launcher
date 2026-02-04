import { useEffect, useRef } from 'react'
import { useTranslation } from '../lib/i18n'

interface ConfirmModalProps {
  open: boolean
  title: string
  message: string
  confirmLabel?: string
  confirmVariant?: 'primary' | 'danger'
  loading?: boolean
  error?: string | null
  onConfirm: () => void
  onCancel: () => void
}

export function ConfirmModal({
  open,
  title,
  message,
  confirmLabel,
  confirmVariant = 'primary',
  loading = false,
  error,
  onConfirm,
  onCancel,
}: ConfirmModalProps) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (open && !dialog.open) {
      dialog.showModal()
    } else if (!open && dialog.open) {
      dialog.close()
    }
  }, [open])

  const confirmStyles =
    confirmVariant === 'danger'
      ? 'bg-destructive text-white hover:bg-destructive/90'
      : 'bg-primary text-primary-foreground hover:bg-primary/90'

  return (
    <dialog
      ref={dialogRef}
      className="fixed inset-0 z-50 m-auto max-w-md rounded-lg border border-border bg-card p-0 text-foreground backdrop:bg-black/50"
      onClose={onCancel}
    >
      <div className="p-6">
        <h2 className="text-lg font-semibold">{title}</h2>
        <p className="mt-2 text-sm text-muted-foreground">{message}</p>
        {error && (
          <p className="mt-2 rounded-md bg-destructive/10 border border-destructive/20 px-3 py-2 text-sm text-destructive">
            {error}
          </p>
        )}
        <div className="mt-6 flex justify-end gap-3">
          <button
            type="button"
            className="rounded-md border border-border px-4 py-2 text-sm hover:bg-muted"
            onClick={onCancel}
            disabled={loading}
          >
            {t('common.cancel')}
          </button>
          <button
            type="button"
            className={`rounded-md px-4 py-2 text-sm font-medium ${confirmStyles} disabled:opacity-50`}
            onClick={onConfirm}
            disabled={loading}
          >
            {loading ? t('common.working') : (confirmLabel || t('common.confirm'))}
          </button>
        </div>
      </div>
    </dialog>
  )
}
