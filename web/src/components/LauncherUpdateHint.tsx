import { useState } from 'react'
import { useTranslation } from '../lib/i18n'
import { useUpdateStore } from '../lib/updateStore'
import { isDesktopModeSync } from '../lib/desktop'
import { UpdateDialog } from './UpdateDialog'

/** Command the server-mode operator runs on the host to update the launcher. */
const UPDATE_CMD = 'citeck update'

/**
 * "This dependency needs a newer launcher" — said in the terms of whoever is
 * looking at it. The desktop app can update itself, so when an update is
 * actually available it offers the button that opens the update dialog; the
 * server Web UI cannot (the daemon is updated from the host), so it names the
 * command instead of offering a button that would do nothing. Desktop with no
 * update available is the third case: there is nothing to install yet.
 */
export function LauncherUpdateHint() {
  const { t } = useTranslation()
  const updateAvailable = useUpdateStore((s) => s.status?.available ?? false)
  const [dialogOpen, setDialogOpen] = useState(false)

  if (isDesktopModeSync() && updateAvailable) {
    return (
      <>
        <button
          type="button"
          className="shrink-0 rounded border border-current/40 px-2 py-0.5 text-xs hover:bg-current/10"
          onClick={() => setDialogOpen(true)}
        >
          {t('deps.updateLauncher')}
        </button>
        {dialogOpen && <UpdateDialog open onClose={() => setDialogOpen(false)} />}
      </>
    )
  }

  return (
    <span className="shrink-0 text-xs opacity-80">
      {isDesktopModeSync() ? t('deps.updateLauncherHint') : t('deps.updateLauncherCli', { cmd: UPDATE_CMD })}
    </span>
  )
}
