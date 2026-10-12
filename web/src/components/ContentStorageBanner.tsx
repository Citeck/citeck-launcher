import { useState } from 'react'
import { AlertTriangle, Loader2, RotateCw } from 'lucide-react'
import { useDashboardStore } from '../lib/store'
import { useTranslation } from '../lib/i18n'
import { postContentStorageProvision } from '../lib/api'
import { toast } from '../lib/toast'
import {
  STATE_ERROR,
  contentStorageProblemText,
} from '../lib/contentStorage'

/**
 * Namespace-level banner for `NamespaceDto.contentStorage` — present only on a
 * namespace that keeps ecos-content's files in S3. Two alert variants, in order of
 * precedence:
 *
 *  - `problem`: this generation could not wire ecos-content to its provider at
 *    all (the workspace stopped declaring it, its secret is unreadable, the
 *    bundle lost ecos-content). NOT dismissible, like BundleErrorBanner: it is
 *    broken right now, and no retry can fix configuration.
 *  - state `error`: the one-time connection inside ECOS failed. NOT
 *    dismissible either, and it carries the Retry button — the daemon also
 *    retries on its own on a five-minute timer and qualifying events.
 * Normal pending / waiting / provisioning progress and settled states render
 * nothing. Only configuration problems and failed connections need attention.
 *
 * The state changes arrive as `namespace_content_storage` SSE events, which
 * the store answers with a namespace refetch — so this banner follows the
 * store and never latches.
 */
export function ContentStorageBanner() {
  const cs = useDashboardStore((s) => s.namespace?.contentStorage)
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

  return null
}
