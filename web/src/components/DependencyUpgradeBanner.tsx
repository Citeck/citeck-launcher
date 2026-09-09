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

/** How often the pending-rollback alarm re-reads the state that raised it. It
 *  changes only when a launcher start retries the rollback, so this is about
 *  taking a stale alarm down promptly, not about catching a fast transition. */
export const ROLLBACK_POLL_MS = 10_000

/**
 * Tells the user that a bundle offers a newer version of an infra dependency
 * which the launcher deliberately did NOT apply (it would be a breaking change
 * for the namespace's data). Dismissible per session; comes back when the set
 * of offered upgrades changes. Two wordings: the launcher can migrate it, or a
 * newer launcher is needed.
 *
 * It also owns the TRIGGERS for GET /namespace/dependencies (the request
 * itself belongs to the store, which serves the dialog the same one when both
 * ask on the same trigger), because the payload carries state no other fetch
 * does — the per-dependency list and the last verdict.
 *
 * The one state it must never hide is a PENDING ROLLBACK: an interrupted
 * migration whose rollback has not succeeded freezes the version pin and
 * refuses every new migration AND every start of the namespace, and the user
 * has to know without opening anything. It reaches the store from two writers
 * — that GET and the ordinary namespace fetch — so the alarm no longer waits
 * for a remount. That variant is NOT dismissible (same rule as
 * BundleErrorBanner: it describes something that is broken right now).
 */
export function DependencyUpgradeBanner({ onDetails }: Props) {
  const upgrades = useDashboardStore((s) => s.namespace?.dependencyUpgrades)
  const nsID = useDashboardStore((s) => s.namespace?.id ?? '')
  const dismissedKey = useDepsStore((s) => s.dismissedKey)
  const dismissBanner = useDepsStore((s) => s.dismissBanner)
  const refresh = useDepsStore((s) => s.refresh)
  // One field, two writers: GET /namespace/dependencies and the ordinary
  // namespace fetch (NamespaceDto.dependencyRollbackPending) — so the alarm
  // goes up on whichever ran last instead of waiting for a remount.
  const rollbackPending = useDepsStore((s) => s.rollbackPending)
  // A finished migration is what CREATES or CLEARS a pending rollback, so the
  // verdict's timestamp is the trigger to look again.
  const resultAt = useDepsStore((s) => s.result?.at ?? 0)
  const { t } = useTranslation()

  // A bundle offering something OLDER than the data is NOT an upgrade: there
  // is nothing to migrate and nothing to wait for, so advertising it would
  // make the user open a dialog to learn there is nothing to do. It is left
  // out before the key is computed as well, or its appearance would resurrect
  // a banner the user dismissed for a set that has not changed.
  const offered = (upgrades ?? []).filter((u) => !u.bundleOlder)
  const upgradeKey = upgradeSetKey(offered)
  useEffect(() => {
    // Best-effort: the daemon answers this from memory, and a failure here has
    // no user-facing action (the dialog reports its own). The store serves the
    // dialog from the same request when it asks on the same trigger.
    void refresh().catch(() => {})
  }, [refresh, nsID, resultAt, upgradeKey])

  // While the alarm is UP, poll — still, even though the namespace fetch now
  // raises AND clears it. A pending rollback is cleared by the daemon's
  // load-time recovery (`recoverInterruptedMigration`), which emits no
  // deps_migration_* event and produces no result, so nothing pushes a frame:
  // the namespace fetch runs on SSE events and, with a healthy stream, its
  // poll fallback stays dormant. The namespace of a failed rollback is not
  // handed back running either, so there may be no events at all — the fetch
  // that would take the banner down is exactly the one that never happens.
  // This is the recovery path for that case, and it costs one request per
  // interval only while a deliberately non-dismissible red banner is up.
  //
  // A poll rather than "follow every namespace fetch": following the store's
  // namespace identity cost one dependencies GET per running app per 5s
  // (`fetchData` publishes a new namespace object on each `app_stats`), i.e.
  // several a second on a 24-app stand, forever.
  useEffect(() => {
    if (!rollbackPending) return
    const timer = setInterval(() => { void refresh().catch(() => {}) }, ROLLBACK_POLL_MS)
    return () => clearInterval(timer)
  }, [refresh, rollbackPending])

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

  if (!offered.length || dismissedKey === upgradeKey) return null

  // Three groups, because there are three different answers. `migratable` is
  // FALSE for a vendor-blocked pair as well as for a dependency this launcher
  // has no plan for, so it cannot tell them apart on its own — and promising
  // "update the launcher" for a hop no launcher will ever take sends the
  // operator somewhere that cannot help.
  const migratable = offered.filter((u) => u.migratable && !u.blocked)
  const blocked = offered.filter((u) => !!u.blocked)
  const needLauncher = offered.filter((u) => !u.migratable && !u.blocked)
  const list = (xs: DependencyUpgradeDto[]) => xs.map((u) => `${u.id} ${u.from} → ${u.to}`).join(', ')
  const groups = [
    migratable.length > 0 && t('deps.banner.available', { list: list(migratable) }),
    blocked.length > 0 && t('deps.banner.blocked', { list: list(blocked) }),
    needLauncher.length > 0 && t('deps.banner.needLauncher', { list: list(needLauncher) }),
  ].filter(Boolean)

  return (
    <div
      role="status"
      className="flex shrink-0 items-center gap-2 border-b border-sky-500/40 bg-sky-500/10 px-3 py-1.5 text-xs text-sky-700 dark:text-sky-300"
    >
      <ArrowUpCircle size={14} className="shrink-0" />
      <span className="min-w-0 flex-1 truncate">{groups.join(' · ')}</span>
      {needLauncher.length > 0 && <LauncherUpdateHint />}
      <button type="button" className="shrink-0 rounded px-2 py-0.5 hover:bg-sky-500/20" onClick={onDetails}>
        {t('deps.banner.details')}
      </button>
      <button
        type="button"
        aria-label={t('deps.banner.dismiss')}
        title={t('deps.banner.dismiss')}
        className="shrink-0 rounded p-0.5 hover:bg-sky-500/20"
        onClick={() => dismissBanner(upgradeKey)}
      >
        <X size={14} />
      </button>
    </div>
  )
}
