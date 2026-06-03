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

  if (!status?.available) return null

  return (
    <>
      <button
        type="button"
        className="relative p-1.5 text-muted-foreground hover:text-foreground hover:bg-muted"
        title={t('update.newVersion', { version: status.latestVersion ?? '' })}
        onClick={() => setOpen(true)}
      >
        <Download size={14} />
        <span className="absolute right-1 top-1 h-1.5 w-1.5 rounded-full bg-emerald-500" />
      </button>
      <UpdateDialog open={open} onClose={() => setOpen(false)} />
    </>
  )
}
