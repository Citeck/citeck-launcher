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
 * Anti-nag: the reconciler keeps retrying and re-emitting the event, so a host
 * the user dismissed is remembered and not re-opened until its apps recover
 * (the host drops out of pullAuthRequired), at which point a fresh failure
 * auto-opens again.
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
  // Hosts the user dismissed without saving — don't auto-reopen for them.
  const dismissedRef = useRef<Set<string>>(new Set())

  // Forget dismissals for hosts that no longer need credentials, so a later
  // failure on the same host auto-opens again.
  useEffect(() => {
    const active = new Set(hosts)
    for (const h of dismissedRef.current) {
      if (!active.has(h)) dismissedRef.current.delete(h)
    }
  }, [hosts])

  // Auto-open the dialog for the first not-yet-dismissed host.
  useEffect(() => {
    if (openHost) return
    const next = hosts.find((h) => !dismissedRef.current.has(h))
    if (next) {
      setOpenHost(next)
    }
  }, [hosts, openHost])

  if (hosts.length === 0) return null

  function dismiss() {
    if (openHost) dismissedRef.current.add(openHost)
    setOpenHost(null)
  }

  async function saved() {
    const host = openHost
    setOpenHost(null)
    if (host) {
      // The daemon already retried pull-failed apps when the binding was
      // saved; clear the per-app markers for this host and forget the
      // dismissal so the banner reflects the new state.
      for (const [app, h] of Object.entries(pullAuthRequired)) {
        if (h === host) clearPullAuthRequired(app)
      }
      dismissedRef.current.delete(host)
    }
    try {
      await postAppsRetryPullFailed()
    } catch {
      /* daemon retries on binding save too — best-effort */
    }
  }

  return (
    <>
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
          onClick={() => setOpenHost(hosts[0])}
        >
          {t('dashboard.registryAuth.action')}
        </button>
      </div>
      <RegistryCredentialsDialog
        open={!!openHost}
        host={openHost ?? ''}
        onClose={dismiss}
        onSaved={saved}
      />
    </>
  )
}
