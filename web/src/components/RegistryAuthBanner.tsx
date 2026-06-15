import { useEffect, useMemo, useRef, useState } from 'react'
import { KeyRound } from 'lucide-react'
import { useDashboardStore } from '../lib/store'
import { useTranslation } from '../lib/i18n'
import { postAppsRetryPullFailed } from '../lib/api'
import { RegistryCredentialsDialog } from './RegistryCredentialsDialog'

/**
 * Namespace-level registry-auth prompt. When one or more app image pulls fail
 * with an auth error the daemon emits `pull_auth_required` (host per app); this
 * surfaces a persistent banner naming the affected hosts and AUTO-OPENS the
 * credentials dialog once per host — so the failure is never silent (the old
 * behaviour just stalled the namespace with a tiny inline table button).
 *
 * Anti-nag / stability: `pullAuthRequired` is noisy — every reconciler retry
 * (and the server-side retry triggered when a secret is created) briefly clears
 * a host's marker as its app leaves PULL_FAILED, then re-adds it when the pull
 * fails again. So:
 *   - The DIALOG's lifetime is driven by `openHost` (component state), NOT by
 *     the current host set — otherwise it (and the nested "create secret"
 *     modal) would unmount mid-edit whenever the marker blinks. This was the
 *     cause of the dialog closing on focus-switch / on secret create.
 *   - Each host AUTO-opens at most once (handledRef); re-access after that is
 *     via the persistent banner, so a blinking marker never re-pops the dialog.
 */
export function RegistryAuthBanner() {
  const pullAuthRequired = useDashboardStore((s) => s.pullAuthRequired)
  const clearPullAuthRequired = useDashboardStore((s) => s.clearPullAuthRequired)
  const { t } = useTranslation()

  // Distinct hosts needing credentials (stable identity between store updates).
  const hosts = useMemo(
    () => Array.from(new Set(Object.values(pullAuthRequired).filter(Boolean))),
    [pullAuthRequired],
  )

  const [openHost, setOpenHost] = useState<string | null>(null)
  // Hosts already auto-opened — never auto-open the same host twice (the marker
  // blinks on every retry; re-access is via the banner button).
  const handledRef = useRef<Set<string>>(new Set())
  // Rotates through the failing hosts on repeated banner-button clicks so every
  // host is reachable (auto-open fires once per host).
  const cursorRef = useRef(0)

  // Auto-open the dialog once for the first not-yet-handled host.
  useEffect(() => {
    if (openHost) return
    const next = hosts.find((h) => !handledRef.current.has(h))
    if (next) {
      handledRef.current.add(next)
      setOpenHost(next)
    }
  }, [hosts, openHost])

  function dismiss() {
    setOpenHost(null)
  }

  async function saved() {
    const host = openHost
    setOpenHost(null)
    if (host) {
      // The daemon already retried pull-failed apps when the binding was saved;
      // clear the per-app markers for this host so the banner reflects it.
      for (const [app, h] of Object.entries(pullAuthRequired)) {
        if (h === host) clearPullAuthRequired(app)
      }
    }
    try {
      await postAppsRetryPullFailed()
    } catch {
      /* daemon retries on binding save too — best-effort */
    }
  }

  return (
    <>
      {hosts.length > 0 && (
        <div
          role="alert"
          className="flex shrink-0 items-center gap-2 border-b border-amber-500/40 bg-amber-500/15 px-3 py-1.5 text-xs text-amber-600 dark:text-amber-400"
        >
          <KeyRound size={14} className="shrink-0" />
          <span className="min-w-0 flex-1 truncate" title={hosts.join(', ')}>
            {t('dashboard.registryAuth.message', { hosts: hosts.join(', ') })}
          </span>
          <button
            type="button"
            className="shrink-0 rounded border border-amber-500/40 px-2 py-0.5 font-medium hover:bg-amber-500/20"
            // Step through all failing hosts on repeated clicks (auto-open only
            // fires once per host, so pinning to hosts[0] would strand the
            // rest). Mark the chosen host handled so dismissing it doesn't
            // immediately re-pop via the auto-open effect (anti-nag).
            onClick={() => {
              const host = hosts[cursorRef.current % hosts.length]
              cursorRef.current += 1
              handledRef.current.add(host)
              setOpenHost(host)
            }}
          >
            {t('dashboard.registryAuth.action')}
          </button>
        </div>
      )}
      {/* Driven by openHost (not hosts) so a blinking marker can't unmount the
          dialog or its nested create-secret modal while the user is editing. */}
      <RegistryCredentialsDialog
        open={!!openHost}
        host={openHost ?? ''}
        onClose={dismiss}
        onSaved={saved}
      />
    </>
  )
}
