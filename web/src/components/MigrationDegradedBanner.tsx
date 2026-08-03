import { useEffect, useState } from 'react'
import { AlertTriangle, X } from 'lucide-react'
import { getMigrationStatus, type DegradedMigrationDto } from '../lib/api'
import { useTranslation } from '../lib/i18n'

/**
 * Warns that the 1.x -> 2.x migration was LOSSY.
 *
 * When the pure-Go H2 reader cannot open the Kotlin `storage.db`, the launcher
 * silently falls back to reconstructing what it can from the filesystem. That
 * fallback recovers no secrets at all and only part of each workspace, so the
 * result looks exactly like a fresh install — which is precisely how a user
 * lost an enterprise workspace without ever being told.
 *
 * There was no surface for this: the only migration signal the UI had was
 * `hasPendingSecrets`, and that is false *precisely when* the fallback ran (the
 * pending-secrets blob is written only on the H2 path), so the unlock prompt
 * never appeared either.
 *
 * Dismissal is per-session on purpose. The condition is permanent — it
 * describes something that already happened — so persisting the dismissal would
 * mean a user who closes it once never sees it again, while the underlying data
 * is still missing.
 */
export function MigrationDegradedBanner() {
  const { t } = useTranslation()
  const [info, setInfo] = useState<DegradedMigrationDto | null>(null)
  const [dismissed, setDismissed] = useState(false)

  useEffect(() => {
    let cancelled = false
    getMigrationStatus()
      .then((s) => {
        if (!cancelled && s.migrationDegraded && s.degradedMigration) {
          setInfo(s.degradedMigration)
        }
      })
      .catch(() => { /* status is best-effort; never block the app on it */ })
    return () => { cancelled = true }
  }, [])

  if (!info || dismissed) return null

  return (
    <div
      role="alert"
      className="flex items-start gap-3 border-b border-warning/40 bg-warning/10 px-4 py-2.5 text-sm"
    >
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warning" aria-hidden />
      <div className="min-w-0 flex-1">
        {/* Two different failures reach this banner and they need different
            wording. `partial`: the store DID open and most data came through,
            only some sub-trees were undecodable. Otherwise: the store could not
            be opened at all and everything was rebuilt from disk. Telling a
            partial-read user "the old database could not be read" would be
            plainly wrong and send them chasing the wrong problem. */}
        <div className="font-medium">
          {t(info.partial ? 'migrationDegraded.titlePartial' : 'migrationDegraded.title')}
        </div>
        <div className="text-xs text-muted-foreground">
          {info.partial
            ? t('migrationDegraded.bodyPartial', {
              entries: String(info.lostEntries ?? 0),
              subtrees: String(info.lostSubtrees ?? 0),
              // `lostFields` is namespaced for the record ("map.<name>");
              // the "map." prefix is internal bookkeeping, not something to
              // show a user hunting for a missing workspace.
              maps: (info.lostFields ?? []).map((f) => f.replace(/^map\./, '')).join(', ') || '—',
            })
            : t('migrationDegraded.body', {
              workspaces: String(info.workspaces),
              namespaces: String(info.namespaces),
              recovered: String(info.recoveredRepoUrls),
            })}
        </div>
        {info.backupPath && (
          <div className="mt-1 text-xs text-muted-foreground">
            {t('migrationDegraded.backup', { path: info.backupPath })}
          </div>
        )}
        <div className="mt-1 text-xs text-muted-foreground">
          {t('migrationDegraded.reason', { reason: info.reason })}
        </div>
      </div>
      <button
        type="button"
        className="shrink-0 rounded p-1 hover:bg-muted"
        onClick={() => setDismissed(true)}
        aria-label={t('common.close')}
      >
        <X className="h-4 w-4" />
      </button>
    </div>
  )
}
