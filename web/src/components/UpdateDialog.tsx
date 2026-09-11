import { useCallback, useEffect, useRef, useState } from 'react'
import { Download, Loader2 } from 'lucide-react'
import { useModalDialog } from '../hooks/useModalDialog'
import Markdown from 'react-markdown'
import { useTranslation } from '../lib/i18n'
import { useUpdateStore } from '../lib/updateStore'
import { getUpdateChangelog, getUpdateStatus, applyUpdate, openExternal } from '../lib/api'
import type { ReleaseNoteDto } from '../lib/types'

interface UpdateDialogProps {
  open: boolean
  onClose: () => void
}

/**
 * How the dialog leaves its "installing" state.
 *
 * `applyUpdate()` returns as soon as the payload is staged — the swap itself
 * happens on the wrapper, which restarts the daemon and then reloads the
 * webview. So the page's exit from `applying` used to be an event it neither
 * controlled nor could observe, with no timeout and with Cancel disabled
 * meanwhile. When the reload did not arrive (on macOS it never did: Wails'
 * `WebviewWindow.Reload` is an unimplemented stub there, fixed separately in
 * the wrapper) the launcher was bricked behind a modal whose every button was
 * disabled, until the user quit and relaunched it. Reported against 2.11.0.
 *
 * The dialog therefore watches for the swap itself, on a CONDITION rather than
 * a delay: the running daemon's own `currentVersion` changing is proof the new
 * one is up and serving, at which point reloading is safe and correct. The
 * timeout below is only a backstop for "no swap ever happened", and it ends in
 * a readable notice rather than a blind reload onto a daemon that may be gone.
 */
const SWAP_POLL_INTERVAL_MS = 2_000
/**
 * Backstop, comfortably past the wrapper's own health gate: it allows the
 * swap 60s to come up (desktop.UpdateHealthTimeout) and, when that fails, the
 * same again for the rollback restart.
 */
const SWAP_WATCHDOG_MS = 150_000

export function UpdateDialog({ open, onClose }: UpdateDialogProps) {
  const { t, locale } = useTranslation()
  const status = useUpdateStore((s) => s.status)
  const check = useUpdateStore((s) => s.check)
  const refresh = useUpdateStore((s) => s.refresh)
  const setStatus = useUpdateStore((s) => s.setStatus)
  const available = !!status?.available
  /**
   * The update was staged and applied, the health gate failed, and the wrapper
   * rolled the daemon back. Same spelling as UpdateNotification's dot, on
   * purpose: the two must not drift, and branching on `available` ALONE is what
   * produced the field report — a dialog headlined "You are on the latest
   * version (2.11.5)" with "Update failed: 2.11.7" printed underneath it.
   *
   * `available` stays false here because the daemon blacklists a failed release
   * (so the badge cannot nag), which is exactly why this state needs its own
   * headline and its own explicit retry rather than the Install button.
   */
  const rolledBack = !available && !!status?.applyError
  // Signature classification (e.g. signing-key rotation): auto-install would
  // keep failing, so the dialog swaps the Install button for a calm
  // manual-download notice. The changelog stays visible.
  const manualUpdate = !!status?.manualUpdateRequired
  const releasesUrl = status?.releasesUrl
  const dialogRef = useModalDialog(open)
  const [notes, setNotes] = useState<ReleaseNoteDto[]>([])
  const [loading, setLoading] = useState(false)
  const [applying, setApplying] = useState(false)
  const [swapStalled, setSwapStalled] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const swapTimers = useRef<ReturnType<typeof setTimeout>[]>([])

  const clearSwapTimers = useCallback(() => {
    swapTimers.current.forEach(clearTimeout)
    swapTimers.current = []
  }, [])

  // Never leave a poll or a watchdog running past this dialog.
  useEffect(() => clearSwapTimers, [clearSwapTimers])

  const loadChangelog = useCallback(() => {
    setLoading(true)
    setError(null)
    getUpdateChangelog(locale)
      .then(setNotes)
      .catch((e) => setError(String(e?.message ?? e)))
      .finally(() => setLoading(false))
  }, [locale])

  // Intentional: one-shot loading flag for the on-open changelog fetch; not a
  // cascading render.
  // eslint-disable-next-line react-hooks/set-state-in-effect
  useEffect(() => { if (open) loadChangelog() }, [open, loadChangelog])

  // Poll the daemon until the one answering is a DIFFERENT build than the one
  // we asked to replace itself — that is the swap, observed rather than
  // assumed — then reload onto it. A daemon that is mid-restart simply fails
  // the request; that is not an answer, so keep waiting for the watchdog.
  const watchForSwap = useCallback((versionBefore: string) => {
    clearSwapTimers()
    let done = false

    const finish = (fn: () => void) => {
      if (done) return
      done = true
      clearSwapTimers()
      fn()
    }

    const poll = () => {
      if (done) return
      getUpdateStatus()
        .then((s) => {
          // Keep every answer, not just the one that ends the wait. The poll
          // used to DISCARD a status whose currentVersion was unchanged, so a
          // rollback that happened 10s in stayed invisible and the header went
          // on describing the world as it was at click time for the full 150s.
          setStatus(s)
          if (s.currentVersion && s.currentVersion !== versionBefore) {
            finish(() => window.location.reload())
          }
        })
        .catch(() => { /* daemon restarting — keep waiting */ })
        .finally(() => {
          if (!done) swapTimers.current.push(setTimeout(poll, SWAP_POLL_INTERVAL_MS))
        })
    }

    swapTimers.current.push(setTimeout(poll, SWAP_POLL_INTERVAL_MS))
    swapTimers.current.push(setTimeout(() => {
      finish(() => {
        setApplying(false)
        setSwapStalled(true)
        // Ask the daemon once more before the banner picks its sentence: "still
        // staged" and "rolled back" look identical from here, and only one of
        // them can be fixed by restarting. refresh() never throws (the store
        // swallows it), so this needs no catch and stays a best-effort refresh.
        void refresh()
      })
    }, SWAP_WATCHDOG_MS))
  }, [clearSwapTimers, refresh, setStatus])

  const onInstall = async (retry = false) => {
    const versionBefore = status?.currentVersion ?? ''
    setApplying(true)
    setSwapStalled(false)
    setError(null)
    try {
      // `retry` is only ever true because the user pressed "Try again" on a
      // release the daemon has blacklisted; nothing schedules it.
      await applyUpdate(retry)
      // The payload is staged; the wrapper is now swapping the daemon under
      // us. Watch for the daemon that comes back rather than trusting the
      // wrapper to reload us — see SWAP_POLL_INTERVAL_MS above.
      watchForSwap(versionBefore)
    } catch (e) {
      setApplying(false)
      clearSwapTimers()
      // A failed staging may have raised the manual-update classification
      // (signature path). Refresh first; when the calm notice takes over,
      // don't also surface the raw error text.
      await refresh()
      if (!useUpdateStore.getState().status?.manualUpdateRequired) {
        setError(String((e as Error)?.message ?? e))
      }
    }
  }

  return (
    <dialog
      ref={dialogRef}
      className="fixed inset-0 z-50 m-auto max-w-2xl rounded-lg border border-border bg-card p-0 text-foreground shadow-xl"
      onClose={onClose}
    >
      <div className="flex max-h-[80vh] flex-col p-6">
        <h2 className="text-lg font-semibold">
          {available
            ? t('update.title', { version: status?.latestVersion ?? '' })
            : rolledBack
              ? t('update.rolledBackTitle', { version: status?.applyError ?? '' })
              : t('update.upToDate', { version: status?.currentVersion ?? '' })}
        </h2>
        {available && (
          <p className="mt-1 text-sm text-muted-foreground">
            {t('update.fromTo', {
              current: status?.currentVersion ?? '',
              latest: status?.latestVersion ?? '',
            })}
          </p>
        )}
        {rolledBack && (
          <p className="mt-1 text-sm text-muted-foreground">
            {t('update.rolledBackHint', { current: status?.currentVersion ?? '' })}
          </p>
        )}

        {/* The payload was staged but no newer daemon ever answered. Say so
            plainly and hand back a way out, instead of spinning forever with
            every control disabled — the shape of the 2.11.0 macOS report. */}
        {swapStalled && (
          <div className="mt-3 rounded-md border border-primary/30 bg-primary/10 px-3 py-3 text-sm">
            {/* "Restart the launcher to finish installing it" is TRUE only
                while the payload is still staged. After a rollback it is advice
                that cannot work — SelectBestEntry never picks a `failed` entry,
                so no number of restarts installs it. The watchdog refreshes the
                status first (see watchForSwap) precisely so this branch is
                answered by the daemon rather than guessed; the retry itself is
                offered by the button row below. */}
            <p>{rolledBack ? t('update.stalledRolledBack') : t('update.restartRequired')}</p>
          </div>
        )}

        {/* A failure that is not the CURRENT state of the dialog: an older
            release rolled back while a newer one is now on offer. When the
            rollback IS the current state the headline already names it, and
            printing it twice is how the contradictory dialog read. */}
        {status?.applyError && !rolledBack && (
          <p className="mt-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {t('update.failed', { error: status.applyError })}
          </p>
        )}

        {/* Calm manual-update notice (signature classification, e.g. a
            signing-key rotation). Info-styled — deliberately not an error:
            nothing is broken, this binary just can't take this release
            automatically. */}
        {manualUpdate && (
          <div className="mt-3 rounded-md border border-primary/30 bg-primary/10 px-3 py-3 text-sm">
            <p>{t('update.manualNotice')}</p>
            {releasesUrl && (
              <button
                type="button"
                className="mt-3 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:opacity-90"
                onClick={() => void openExternal(releasesUrl)}
              >
                {t('update.openReleases')}
              </button>
            )}
          </div>
        )}

        {/* Changelog (what's new) only matters when an update is available.
            When already on the latest version there is nothing newer to list,
            so skip the box entirely instead of showing a confusing
            "no changelog available" line. */}
        {available && (
        <div className="mt-4 flex-1 overflow-auto rounded-md border border-border bg-background p-4">
          {loading && <p className="text-sm text-muted-foreground">{t('update.loadingChangelog')}</p>}
          {/* A failed fetch now reaches this branch (the daemon stopped
              swallowing a 404 on the index at 2.4.0+), so it has to read as a
              recoverable failure, not a wall of raw text: a localized line, the
              transport detail kept in the tooltip for diagnosis, and the same
              Retry the empty state offers. */}
          {!loading && error && (
            <div className="flex items-center gap-3">
              <p className="text-sm text-destructive" title={error}>{t('update.changelogFailed')}</p>
              <button
                type="button"
                className="rounded-md border border-border px-2 py-1 text-xs hover:bg-muted"
                onClick={loadChangelog}
              >
                {t('common.retry')}
              </button>
            </div>
          )}
          {/* An empty list is genuinely ambiguous: the daemon reports "no notes"
              both when a release truly has none (a tag predating the changelog
              feature) AND when changelog/index.json could not be fetched — a
              404 there is deliberately not treated as an error. Right after a
              release the second case is the likely one, and the dialog only
              fetches on open, so without a retry here the message is a dead end
              that outlives the condition that caused it. */}
          {!loading && !error && notes.length === 0 && (
            <div className="flex items-center gap-3">
              <p className="text-sm text-muted-foreground">{t('update.noChangelog')}</p>
              <button
                type="button"
                className="rounded-md border border-border px-2 py-1 text-xs hover:bg-muted"
                onClick={loadChangelog}
              >
                {t('common.retry')}
              </button>
            </div>
          )}
          {!loading &&
            !error &&
            notes.map((n) => (
              <div key={n.version} className="mb-4">
                <div className="mb-1 flex items-baseline gap-2">
                  <span className="font-semibold">{n.version}</span>
                  <span className="text-xs text-muted-foreground">{n.date}</span>
                </div>
                <div className="prose prose-sm prose-invert max-w-none text-sm">
                  <Markdown>{n.markdown}</Markdown>
                </div>
              </div>
            ))}
        </div>
        )}

        <div className="mt-6 flex items-center justify-end gap-3">
          <button
            type="button"
            className="rounded-md border border-border px-4 py-2 text-sm hover:bg-muted disabled:opacity-50"
            // Re-check the release AND re-fetch the notes. Checking alone left
            // the one affordance offered next to an empty changelog unable to
            // fix it: `check()` only refreshes the release status, and the
            // notes are loaded solely by the on-open effect. Order matters —
            // the daemon's Changelog() reads the cached latest, so refresh that
            // first.
            onClick={() => { void check().then(loadChangelog).catch(loadChangelog) }}
            disabled={applying}
          >
            {t('update.checkNow')}
          </button>
          {/* Deliberately NOT disabled while applying. Closing the dialog does
              not cancel anything — the swap runs on the wrapper — and this was
              the last exit from a modal that could otherwise sit in `applying`
              forever, taking the whole launcher with it. Nothing is protected
              by disabling it; a bricked window is what it cost. */}
          <button
            type="button"
            className="rounded-md border border-border px-4 py-2 text-sm hover:bg-muted"
            onClick={onClose}
          >
            {t('common.cancel')}
          </button>
          {/* The way back to a release the daemon has blacklisted. It is the
              ONLY caller that sets the retry flag: the machine may not re-apply
              a failed release, the person may. Hidden under the manual-update
              classification for the same reason Install is — it would fail
              identically. */}
          {rolledBack && !manualUpdate && (
            <button
              type="button"
              className="flex items-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
              onClick={() => void onInstall(true)}
              disabled={applying}
              title={applying ? t('update.installing') : undefined}
            >
              {applying
                ? <Loader2 size={14} className="shrink-0 animate-spin" />
                : <Download size={14} className="shrink-0" />}
              {t('update.tryAgain')}
            </button>
          )}
          {/* Auto-install is hidden under the manual-update classification —
              it would fail the same way again; the notice above offers the
              manual download instead. */}
          {available && !manualUpdate && (
            <button
              type="button"
              className="flex items-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
              onClick={() => void onInstall()}
              disabled={applying}
              title={applying ? t('update.installing') : undefined}
            >
              {/* Progress swaps only the ICON, never the label — same rule as
                  the Update&Start button. "Обновить" → "Обновление…" is a
                  different width, so the right-aligned row re-flowed on click;
                  on the macOS webview that repainted as buttons sitting on top
                  of each other (reported against 2.11.0). */}
              {applying
                ? <Loader2 size={14} className="shrink-0 animate-spin" />
                : <Download size={14} className="shrink-0" />}
              {t('update.install')}
            </button>
          )}
        </div>
      </div>
    </dialog>
  )
}
