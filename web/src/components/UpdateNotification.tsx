import { useEffect, useState } from 'react'
import { Download } from 'lucide-react'
import { useUpdateStore } from '../lib/updateStore'
import { useTranslation } from '../lib/i18n'
import { UpdateDialog } from './UpdateDialog'

const CHECK_INTERVAL_MS = 4 * 60 * 60 * 1000 // 4h, mirrors the daemon's RunPeriodic

export function UpdateNotification() {
  const { t } = useTranslation()
  const status = useUpdateStore((s) => s.status)
  const refresh = useUpdateStore((s) => s.refresh)
  const [open, setOpen] = useState(false)

  useEffect(() => {
    void refresh()
    const id = setInterval(() => void refresh(), CHECK_INTERVAL_MS)
    return () => clearInterval(id)
  }, [refresh])

  // The icon is always present in desktop mode (where the updater runs) so the
  // user can open the dialog and check for updates on demand. status is null in
  // server mode (the endpoint 404s) or before the first check — render nothing.
  if (!status) return null
  const available = !!status.available
  // A rolled-back failure shows a red dot so the user learns about it instead of
  // it silently vanishing; an available update shows a green dot.
  const rolledBack = !available && !!status.applyError
  const showDot = available || rolledBack

  return (
    <>
      <button
        type="button"
        className="relative p-1.5 text-muted-foreground hover:text-foreground hover:bg-muted"
        title={
          available
            ? t('update.newVersion', { version: status.latestVersion ?? '' })
            : rolledBack
              ? t('update.failed', { error: status.applyError ?? '' })
              : t('update.checkNow')
        }
        onClick={() => setOpen(true)}
      >
        <Download size={14} />
        {showDot && (
          <span
            className={`absolute right-1 top-1 h-1.5 w-1.5 rounded-full ${rolledBack ? 'bg-red-500' : 'bg-emerald-500'}`}
          />
        )}
      </button>
      <UpdateDialog open={open} onClose={() => setOpen(false)} />
    </>
  )
}
