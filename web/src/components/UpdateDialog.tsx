import { useCallback, useEffect, useRef, useState } from 'react'
import Markdown from 'react-markdown'
import { useTranslation } from '../lib/i18n'
import { useUpdateStore } from '../lib/updateStore'
import { getUpdateChangelog, applyUpdate } from '../lib/api'
import type { ReleaseNoteDto } from '../lib/types'

interface UpdateDialogProps {
  open: boolean
  onClose: () => void
}

export function UpdateDialog({ open, onClose }: UpdateDialogProps) {
  const { t, locale } = useTranslation()
  const status = useUpdateStore((s) => s.status)
  const check = useUpdateStore((s) => s.check)
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [notes, setNotes] = useState<ReleaseNoteDto[]>([])
  const [loading, setLoading] = useState(false)
  const [applying, setApplying] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    else if (!open && dialog.open) dialog.close()
  }, [open])

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

  const onInstall = async () => {
    setApplying(true)
    setError(null)
    try {
      await applyUpdate()
      // The wrapper swaps the daemon and reloads the webview; nothing more to do.
    } catch (e) {
      setApplying(false)
      setError(String((e as Error)?.message ?? e))
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
          {status?.available
            ? t('update.title', { version: status?.latestVersion ?? '' })
            : t('update.upToDate', { version: status?.currentVersion ?? '' })}
        </h2>
        {status?.available && (
          <p className="mt-1 text-sm text-muted-foreground">
            {t('update.fromTo', {
              current: status?.currentVersion ?? '',
              latest: status?.latestVersion ?? '',
            })}
          </p>
        )}

        {status?.applyError && (
          <p className="mt-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {t('update.failed', { error: status.applyError })}
          </p>
        )}

        {/* Changelog (what's new) only matters when an update is available.
            When already on the latest version there is nothing newer to list,
            so skip the box entirely instead of showing a confusing
            "no changelog available" line. */}
        {status?.available && (
        <div className="mt-4 flex-1 overflow-auto rounded-md border border-border bg-background p-4">
          {loading && <p className="text-sm text-muted-foreground">{t('update.loadingChangelog')}</p>}
          {!loading && error && <p className="text-sm text-destructive">{error}</p>}
          {!loading && !error && notes.length === 0 && (
            <p className="text-sm text-muted-foreground">{t('update.noChangelog')}</p>
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
            onClick={() => void check()}
            disabled={applying}
          >
            {t('update.checkNow')}
          </button>
          <button
            type="button"
            className="rounded-md border border-border px-4 py-2 text-sm hover:bg-muted disabled:opacity-50"
            onClick={onClose}
            disabled={applying}
          >
            {t('common.cancel')}
          </button>
          {status?.available && (
            <button
              type="button"
              className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
              onClick={() => void onInstall()}
              disabled={applying}
            >
              {applying ? t('update.installing') : t('update.install')}
            </button>
          )}
        </div>
      </div>
    </dialog>
  )
}
