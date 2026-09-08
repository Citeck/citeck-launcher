import { useEffect } from 'react'
import { AlertTriangle, ArrowUpCircle, X } from 'lucide-react'
import { useDashboardStore } from '../lib/store'
import { useDepsStore, upgradeSetKey } from '../lib/depsStore'
import { useTranslation } from '../lib/i18n'
import { LauncherUpdateHint } from './LauncherUpdateHint'
import type { DependencyUpgradeDto } from '../lib/types'

interface Props {
  onDetails: () => void
}

/**
 * Tells the user that a bundle offers a newer version of an infra dependency
 * which the launcher deliberately did NOT apply (it would be a breaking change
 * for the namespace's data). Dismissible per session; comes back when the set
 * of offered upgrades changes. Two wordings: the launcher can migrate it, or a
 * newer launcher is needed.
 *
 * It also owns the refresh of GET /namespace/dependencies, because the one
 * state it must never hide — a PENDING ROLLBACK — is not part of NamespaceDto:
 * an interrupted migration whose rollback has not succeeded freezes the
 * version pin and refuses every new migration, and the user has to know
 * without opening anything. That variant is NOT dismissible (same rule as
 * BundleErrorBanner: it describes something that is broken right now).
 */
export function DependencyUpgradeBanner({ onDetails }: Props) {
  const upgrades = useDashboardStore((s) => s.namespace?.dependencyUpgrades)
  const nsID = useDashboardStore((s) => s.namespace?.id ?? '')
  const dismissedKey = useDepsStore((s) => s.dismissedKey)
  const dismissBanner = useDepsStore((s) => s.dismissBanner)
  const refresh = useDepsStore((s) => s.refresh)
  const rollbackPending = useDepsStore((s) => s.data?.rollbackPending ?? '')
  // A finished migration is what CREATES or CLEARS a pending rollback, so the
  // verdict's timestamp is the trigger to look again.
  const resultAt = useDepsStore((s) => s.result?.at ?? 0)
  const { t } = useTranslation()

  useEffect(() => {
    // Best-effort: the daemon answers this from memory, and a failure here has
    // no user-facing action (the dialog reports its own).
    void refresh().catch(() => {})
  }, [refresh, nsID, resultAt])

  if (rollbackPending) {
    return (
      <div
        role="alert"
        className="flex shrink-0 items-center gap-2 border-b border-red-500/40 bg-red-500/15 px-3 py-1.5 text-xs text-red-600 dark:text-red-400"
      >
        <AlertTriangle size={14} className="shrink-0" />
        <span className="min-w-0 flex-1 truncate" title={rollbackPending}>
          {t('deps.rollbackPending', { message: rollbackPending })}
        </span>
        <button type="button" className="shrink-0 rounded px-2 py-0.5 hover:bg-red-500/20" onClick={onDetails}>
          {t('deps.banner.details')}
        </button>
      </div>
    )
  }

  const key = upgradeSetKey(upgrades)
  if (!upgrades?.length || dismissedKey === key) return null

  const migratable = upgrades.filter((u) => u.migratable)
  const needLauncher = upgrades.filter((u) => !u.migratable)
  const list = (xs: DependencyUpgradeDto[]) => xs.map((u) => `${u.id} ${u.from} → ${u.to}`).join(', ')

  return (
    <div
      role="status"
      className="flex shrink-0 items-center gap-2 border-b border-sky-500/40 bg-sky-500/10 px-3 py-1.5 text-xs text-sky-700 dark:text-sky-300"
    >
      <ArrowUpCircle size={14} className="shrink-0" />
      <span className="min-w-0 flex-1 truncate">
        {migratable.length > 0 && t('deps.banner.available', { list: list(migratable) })}
        {migratable.length > 0 && needLauncher.length > 0 && ' · '}
        {needLauncher.length > 0 && t('deps.banner.needLauncher', { list: list(needLauncher) })}
      </span>
      {needLauncher.length > 0 && <LauncherUpdateHint />}
      <button type="button" className="shrink-0 rounded px-2 py-0.5 hover:bg-sky-500/20" onClick={onDetails}>
        {t('deps.banner.details')}
      </button>
      <button
        type="button"
        aria-label={t('deps.banner.dismiss')}
        title={t('deps.banner.dismiss')}
        className="shrink-0 rounded p-0.5 hover:bg-sky-500/20"
        onClick={() => dismissBanner(key)}
      >
        <X size={14} />
      </button>
    </div>
  )
}
