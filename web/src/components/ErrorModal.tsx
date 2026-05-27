import { useEffect, useRef, useState } from 'react'
import { ChevronDown, ChevronRight, Download } from 'lucide-react'
import { useTranslation } from '../lib/i18n'
import { useErrorModalStore } from '../lib/errorModal'
import { getSystemDump } from '../lib/api'
import { toast } from '../lib/toast'

interface ErrorModalProps {
  open: boolean
  onClose: () => void
  title?: string
  message: string
  details?: string
}

export function ErrorModal({ open, onClose, title, message, details }: ErrorModalProps) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [expanded, setExpanded] = useState(false)
  const [dumping, setDumping] = useState(false)

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (open && !dialog.open) {
      dialog.showModal()
    } else if (!open && dialog.open) {
      dialog.close()
    }
    if (!open) setExpanded(false)
  }, [open])

  async function handleDump() {
    setDumping(true)
    try {
      await getSystemDump('zip')
      toast(t('dashboard.systemDump.success'), 'success')
    } catch (e) {
      toast((e as Error).message, 'error')
    } finally {
      setDumping(false)
    }
  }

  return (
    <dialog
      ref={dialogRef}
      className="fixed inset-0 z-50 m-auto max-w-2xl rounded-lg border border-border bg-card p-0 text-foreground backdrop:bg-black/50"
      onClose={onClose}
    >
      <div className="p-6">
        <h2 className="text-lg font-semibold text-destructive">
          {title || t('errorModal.title')}
        </h2>
        <p className="mt-3 whitespace-pre-wrap break-words text-sm text-foreground select-text">
          {message}
        </p>
        {details && (
          <div className="mt-4">
            <button
              type="button"
              className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
              onClick={() => setExpanded((v) => !v)}
            >
              {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
              {t('errorModal.details')}
            </button>
            {expanded && (
              <pre className="mt-2 max-h-64 overflow-auto rounded-md border border-border bg-muted/30 p-3 text-[11px] leading-snug font-mono text-muted-foreground select-text">
                {details}
              </pre>
            )}
          </div>
        )}
        <div className="mt-6 flex justify-end gap-3">
          <button
            type="button"
            className="flex items-center gap-1 rounded-md border border-border px-3 py-2 text-sm hover:bg-muted disabled:opacity-50"
            onClick={handleDump}
            disabled={dumping}
          >
            <Download size={14} />
            {dumping ? t('common.working') : t('errorModal.downloadDump')}
          </button>
          <button
            type="button"
            className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
            onClick={onClose}
          >
            {t('common.close')}
          </button>
        </div>
      </div>
    </dialog>
  )
}

export function ErrorModalHost() {
  const current = useErrorModalStore((s) => s.current)
  const close = useErrorModalStore((s) => s.close)
  return (
    <ErrorModal
      open={current !== null}
      onClose={close}
      title={current?.title}
      message={current?.message ?? ''}
      details={current?.details}
    />
  )
}
