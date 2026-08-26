import { AlertTriangle } from 'lucide-react'
import { useDashboardStore } from '../lib/store'
import { useTranslation } from '../lib/i18n'

/**
 * Namespace-level banner for `NamespaceDto.bundleError`.
 *
 * The daemon has recorded this condition since the empty-bundle work, and it
 * reached both the DTO and `types.ts` — but no component ever read it, so the
 * single most misleading state the launcher can be in was still invisible:
 * every Citeck service comes from the bundle, while the infra apps (postgres,
 * mongo, rabbitmq, zookeeper, mailpit, pgadmin, onlyoffice) are generated
 * unconditionally with hardcoded fallback images. A namespace whose bundle
 * failed to resolve — or resolved to zero applications — therefore starts seven
 * third-party containers, passes every probe and reports RUNNING 7/7, with none
 * of the product in it. That is exactly what "Quick Start gave me only
 * third-party apps" looked like from the inside.
 *
 * Deliberately NOT dismissible, unlike DiskLowBanner (a host condition the
 * operator may knowingly accept) and MigrationDegradedBanner (it describes
 * something that already finished). This one describes the namespace being
 * unusable RIGHT NOW, and hiding it re-creates the very invisibility the banner
 * exists to remove. It costs nothing to leave up: the daemon re-derives the
 * verdict on every load and every reload, so it disappears by itself the moment
 * a resolve produces a bundle with services in it.
 */
export function BundleErrorBanner() {
  const bundleError = useDashboardStore((s) => s.namespace?.bundleError ?? '')
  const { t } = useTranslation()

  if (!bundleError) return null

  return (
    <div
      role="alert"
      className="flex shrink-0 items-start gap-2 border-b border-destructive/40 bg-destructive/10 px-3 py-1.5 text-xs text-destructive"
    >
      <AlertTriangle size={14} className="mt-0.5 shrink-0" />
      <div className="min-w-0 flex-1">
        <div className="font-medium">{t('dashboard.bundleError.title')}</div>
        {/* The reason comes from the daemon verbatim (a resolver error, or the
            "resolved to no applications" verdict). It is the only part that
            says WHICH repository or ref is at fault, so it is shown in full
            rather than truncated to one line. */}
        <div className="text-muted-foreground">{t('dashboard.bundleError.reason', { reason: bundleError })}</div>
        <div className="text-muted-foreground">{t('dashboard.bundleError.hint')}</div>
      </div>
    </div>
  )
}
