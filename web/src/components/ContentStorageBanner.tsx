import { useState } from 'react'
import { AlertTriangle, HardDrive, Loader2, RotateCw, X } from 'lucide-react'
import { useDashboardStore } from '../lib/store'
import { useActiveWorkspaceId } from '../lib/daemonStatus'
import { useTranslation } from '../lib/i18n'
import { postContentStorageProvision } from '../lib/api'
import { toast } from '../lib/toast'
import {
  STATE_ERROR,
  STATE_PENDING,
  STATE_PROVISIONING,
  contentStorageProblemText,
  isContentStorageInProgress,
  useContentStorageBannerStore,
} from '../lib/contentStorage'

/**
 * Namespace-level banner for `NamespaceDto.contentStorage` — present only on a
 * namespace that keeps ecos-content's files in S3. Three variants, in order of
 * precedence:
 *
 *  - `problem`: this generation could not wire ecos-content to its provider at
 *    all (the workspace stopped declaring it, its secret is unreadable, the
 *    bundle lost ecos-content). NOT dismissible, like BundleErrorBanner: it is
 *    broken right now, and no retry can fix configuration.
 *  - state `error`: the one-time connection inside ECOS failed. NOT
 *    dismissible either, and it carries the Retry button — the daemon also
 *    retries on its own on the next qualifying event.
 *  - pending / waiting / provisioning: progress, shown as information and
 *    dismissible for the session. Hidden on a STOPPED namespace, where nothing
 *    is going to happen: the daemon has no persisted "connected" flag and
 *    re-derives the connection from ECOS after every start, so every S3
 *    namespace reads "pending" while stopped, connected or not.
 *
 * connected and admin-managed render nothing: both are settled, and an
 * administrator's own choice of storage is not the launcher's business.
 *
 * The state changes arrive as `namespace_content_storage` SSE events, which
 * the store answers with a namespace refetch — so this banner follows the
 * store and never latches.
 */
export function ContentStorageBanner() {
  const cs = useDashboardStore((s) => s.namespace?.contentStorage)
  const nsId = useDashboardStore((s) => s.namespace?.id ?? '')
  const workspaceId = useActiveWorkspaceId()
  const nsStatus = useDashboardStore((s) => s.namespace?.status)
  const dismissedNs = useContentStorageBannerStore((s) => s.dismissedNs)
  const dismissProgress = useContentStorageBannerStore((s) => s.dismissProgress)
  const [retrying, setRetrying] = useState(false)
  const { t } = useTranslation()

  if (!cs) return null
  // Sentences name the provider by its app id (the row in the app table). It
  // is empty only when the workspace names no provider at all — then a title
  // without it, never a stand-in like "(S3)".
  const app = cs.providerApp

  if (cs.problem) {
    return (
      <div
        role="alert"
        className="flex shrink-0 items-start gap-2 border-b border-destructive/40 bg-destructive/10 px-3 py-1.5 text-xs text-destructive"
      >
        <AlertTriangle size={14} className="mt-0.5 shrink-0" />
        <div className="min-w-0 flex-1">
          <div className="font-medium">
            {app ? t('contentStorage.banner.problem.title', { app }) : t('contentStorage.banner.problem.titleNoApp')}
          </div>
          <div className="text-muted-foreground">{contentStorageProblemText(cs.problem, cs.providerApp, t)}</div>
          <div className="text-muted-foreground">{t('contentStorage.banner.problem.hint')}</div>
        </div>
      </div>
    )
  }

  if (cs.state === STATE_ERROR) {
    const retry = async () => {
      setRetrying(true)
      try {
        await postContentStorageProvision()
        // The verdict arrives as a state change; refetch rather than wait for
        // the event, so the button's click is answered on the next frame.
        void useDashboardStore.getState().fetchData()
      } catch (err) {
        toast((err as Error).message, 'error')
      } finally {
        setRetrying(false)
      }
    }
    return (
      <div
        role="alert"
        className="flex shrink-0 items-start gap-2 border-b border-destructive/40 bg-destructive/10 px-3 py-1.5 text-xs text-destructive"
      >
        <AlertTriangle size={14} className="mt-0.5 shrink-0" />
        <div className="min-w-0 flex-1">
          <div className="font-medium">
            {app ? t('contentStorage.banner.error.title', { app }) : t('contentStorage.banner.error.titleNoApp')}
          </div>
          {/* The daemon's own sentence, already in this reader's language — it
              is the only part that says WHICH step failed. */}
          {cs.error && <div className="text-muted-foreground">{t('contentStorage.banner.error.reason', { reason: cs.error })}</div>}
          <div className="text-muted-foreground">{t('contentStorage.banner.error.hint')}</div>
        </div>
        <button
          type="button"
          className="inline-flex shrink-0 items-center gap-1 rounded px-2 py-0.5 hover:bg-destructive/20 disabled:opacity-50"
          onClick={() => { void retry() }}
          disabled={retrying}
        >
          {retrying ? <Loader2 size={12} className="animate-spin" /> : <RotateCw size={12} />}
          {t('contentStorage.banner.retry')}
        </button>
      </div>
    )
  }

  if (!isContentStorageInProgress(cs.state)) return null
  if (nsStatus === 'STOPPED' || dismissedNs === JSON.stringify([workspaceId, nsId])) return null
  // No provider named, no generation problem: nothing is being connected (the
  // daemon runs nothing without a provider), so there is no progress to show.
  if (!app) return null

  const key = cs.state === STATE_PENDING
    ? 'contentStorage.banner.pending'
    : cs.state === STATE_PROVISIONING
      ? 'contentStorage.banner.provisioning'
      : 'contentStorage.banner.waiting'
  return (
    <div
      role="status"
      className="flex shrink-0 items-center gap-2 border-b border-sky-500/40 bg-sky-500/10 px-3 py-1.5 text-xs text-sky-700 dark:text-sky-300"
    >
      {cs.state === STATE_PENDING
        ? <HardDrive size={14} className="shrink-0" />
        : <Loader2 size={14} className="shrink-0 animate-spin" />}
      <span className="min-w-0 flex-1 truncate" title={t(key, { app })}>{t(key, { app })}</span>
      <button
        type="button"
        aria-label={t('contentStorage.banner.dismiss')}
        title={t('contentStorage.banner.dismiss')}
        className="shrink-0 rounded p-0.5 hover:bg-sky-500/20"
        onClick={() => dismissProgress(JSON.stringify([workspaceId, nsId]))}
      >
        <X size={14} />
      </button>
    </div>
  )
}
