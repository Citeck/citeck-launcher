import { useEffect, useRef } from 'react'
import { postGitSkipPull } from '../lib/api'
import { useTranslation } from '../lib/i18n'

export type GitPullDecision = 'retry' | 'skip' | 'cancel'

interface GitPullErrorDialogProps {
  open: boolean
  repoUrl: string
  errorMessage: string
  skipAvailable: boolean
  cancelAvailable: boolean
  onDecide: (d: GitPullDecision) => void
}

/**
 * Port of Kotlin's `GitPullErrorDialog`.
 *
 * Surfaces a recoverable git pull failure with three actions:
 *  - Retry — re-attempt the pull
 *  - Skip — proceed using the last successful clone (only when one exists,
 *    Kotlin: skipAvailable=true). The host portion of `repoUrl` is posted
 *    to /api/v1/git/skip-pull so the daemon suppresses pull operations
 *    against the same host for the next hour — sibling bundle / workspace
 *    repos hosted there won't re-prompt either (Kotlin parity —
 *    `skipPullForRepoDecisionAt` map).
 *  - Cancel — abort the higher-level operation (only when allowed by caller).
 */
export function GitPullErrorDialog({ open, repoUrl, errorMessage, skipAvailable, cancelAvailable, onDecide }: GitPullErrorDialogProps) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)

  useEffect(() => {
    const d = dialogRef.current
    if (!d) return
    if (open && !d.open) d.showModal()
    else if (!open && d.open) d.close()
  }, [open])

  // Skip handler: fire-and-forget post to the daemon so the host-level
  // suppression takes effect before the caller's retry path re-evaluates.
  // Errors are swallowed (best-effort) — the user already made a decision,
  // and worst case the next pull will just re-prompt.
  const handleSkip = () => {
    const host = extractHost(repoUrl)
    if (host) {
      postGitSkipPull(host, 3600).catch((err) => {
        // Surface in console so QA can spot daemon-side issues, but never
        // block the UI on this side-effect.
         
        console.warn('git skip-pull request failed:', err)
      })
    }
    onDecide('skip')
  }

  return (
    <dialog
      ref={dialogRef}
      className="fixed inset-0 z-50 m-auto max-w-lg w-full rounded-lg border border-border bg-card p-0 text-foreground backdrop:bg-black/50"
    >
      <div className="p-6">
        <h2 className="text-lg font-semibold mb-3">{t('gitPullError.title')}</h2>
        <p className="text-xs text-destructive whitespace-pre-wrap mb-3">{errorMessage}</p>
        <p className="text-sm font-mono text-muted-foreground break-all mb-3">{repoUrl}</p>
        <p className="text-xs text-muted-foreground mb-4">
          {skipAvailable ? t('gitPullError.canSkip') : t('gitPullError.cannotSkip')}
        </p>
        <div className="flex justify-end gap-2">
          {cancelAvailable && (
            <button
              type="button"
              className="rounded-md border border-border px-3 py-1.5 text-sm hover:bg-muted"
              onClick={() => onDecide('cancel')}
            >
              {t('common.cancel')}
            </button>
          )}
          {skipAvailable && (
            <button
              type="button"
              className="rounded-md border border-border px-3 py-1.5 text-sm hover:bg-muted"
              onClick={handleSkip}
            >
              {t('gitPullError.skip')}
            </button>
          )}
          <button
            type="button"
            className="rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-sm font-medium hover:bg-primary/90"
            onClick={() => onDecide('retry')}
          >
            {t('gitPullError.retry')}
          </button>
        </div>
      </div>
    </dialog>
  )
}

/**
 * Extracts the bare hostname (lowercased, no port) from a git URL. Supports
 * https/ssh URLs and the `git@host:path` SCP form. Mirrors the server-side
 * git.HostFromURL helper so the skip request keys on the same string the
 * daemon compares against.
 */
function extractHost(repoUrl: string): string {
  const trimmed = (repoUrl ?? '').trim()
  if (!trimmed) return ''
  // SCP-like form: git@host:user/repo.git
  if (!trimmed.includes('://')) {
    const at = trimmed.indexOf('@')
    if (at < 0) return ''
    const rest = trimmed.slice(at + 1)
    const colon = rest.indexOf(':')
    return (colon >= 0 ? rest.slice(0, colon) : rest).toLowerCase()
  }
  try {
    return new URL(trimmed).hostname.toLowerCase()
  } catch {
    return ''
  }
}
