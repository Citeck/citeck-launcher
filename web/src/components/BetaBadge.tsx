import { useTranslation } from '../lib/i18n'

/**
 * The BETA chip: marks a module the bundle ships as experimental — beside each
 * feature in the namespace dialog, and beside the app in the app table. Amber,
 * like every "mind this" note in the UI, never red: nothing is wrong with the
 * app, it is simply not final.
 */
export function BetaBadge({ title, className = '' }: { title?: string; className?: string }) {
  const { t } = useTranslation()
  return (
    <span
      className={`inline-flex shrink-0 items-center rounded border border-amber-500/40 bg-amber-500/15 px-1 py-px text-[9px] font-semibold uppercase leading-none tracking-wide text-amber-700 dark:text-amber-300 ${className}`}
      title={title}
    >
      {t('experimental.badge')}
    </span>
  )
}
