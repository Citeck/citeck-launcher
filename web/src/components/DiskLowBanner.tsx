import { useDashboardStore } from '../lib/store'
import { useTranslation } from '../lib/i18n'
import { AlertTriangle, X } from 'lucide-react'

/** Format free-space bytes as a short human string ("3.2 GB", "850 MB"). */
function formatFreeSpace(bytes: number): string {
  const gb = bytes / 2 ** 30
  if (gb >= 1) return `${gb.toFixed(1)} GB`
  return `${Math.round(bytes / 2 ** 20)} MB`
}

/**
 * Dismissible amber banner driven by the daemon's `disk_low` / `disk_ok` SSE
 * events (low-disk monitor, state-change emission only). Dismissal hides it
 * until a NEW `disk_low` event arrives — i.e. the disk recovered and tripped
 * again; `disk_ok` clears it outright.
 */
export function DiskLowBanner() {
  const diskLow = useDashboardStore((s) => s.diskLow)
  const dismissDiskLow = useDashboardStore((s) => s.dismissDiskLow)
  const { t } = useTranslation()

  if (!diskLow) return null

  return (
    <div
      role="alert"
      className="flex shrink-0 items-center gap-2 border-b border-amber-500/40 bg-amber-500/15 px-3 py-1.5 text-xs text-amber-600 dark:text-amber-400"
    >
      <AlertTriangle size={14} className="shrink-0" />
      <span className="min-w-0 flex-1 truncate" title={diskLow.path}>
        {t('dashboard.diskLow.message', { free: formatFreeSpace(diskLow.freeBytes), path: diskLow.path })}
      </span>
      <button
        type="button"
        aria-label={t('dashboard.diskLow.dismiss')}
        title={t('dashboard.diskLow.dismiss')}
        className="shrink-0 rounded p-0.5 hover:bg-amber-500/20"
        onClick={dismissDiskLow}
      >
        <X size={14} />
      </button>
    </div>
  )
}
