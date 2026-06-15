import { useRef, useState } from 'react'
import { getMissingRegistryAuth } from '../lib/api'
import { RegistryCredentialsDialog } from './RegistryCredentialsDialog'

/**
 * Pre-start gate for registry credentials. Before a namespace start (Welcome
 * quick-start or the dashboard start button) call `preflight(action)`: if every
 * auth-required registry already has a credential the action runs immediately;
 * otherwise the credentials dialog opens for each missing host in turn and the
 * action runs only once all are resolved. Cancelling any host aborts the start
 * (hard block) — the namespace is never started doomed-to-stall.
 *
 * Usage:
 *   const { preflight, dialog } = useRegistryPreflight()
 *   ... onClick={() => preflight(() => doStart())}
 *   ... return <>{dialog}{rest}</>
 */
export function useRegistryPreflight() {
  const [queue, setQueue] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const pendingAction = useRef<(() => void | Promise<void>) | null>(null)

  async function preflight(action: () => void | Promise<void>) {
    setBusy(true)
    // Default []: a failed check must not block the start on the check itself.
    let missing: string[] = []
    try {
      missing = await getMissingRegistryAuth()
    } catch {
      /* daemon can't tell us — fall through with no missing hosts */
    } finally {
      setBusy(false)
    }
    if (missing.length === 0) {
      await action()
      return
    }
    pendingAction.current = action
    setQueue(missing)
  }

  function runPending() {
    const action = pendingAction.current
    pendingAction.current = null
    setQueue([])
    void action?.()
  }

  function handleSaved() {
    // Re-check: the just-bound host should drop out. Continue with whatever is
    // still missing, or run the pending action when nothing remains.
    getMissingRegistryAuth()
      .then((missing) => {
        if (missing.length === 0) runPending()
        else setQueue(missing)
      })
      .catch(() => runPending()) // can't re-check — don't trap the user
  }

  function handleCancel() {
    // Hard block: cancelling aborts the whole start.
    pendingAction.current = null
    setQueue([])
  }

  const dialog = (
    <RegistryCredentialsDialog
      open={queue.length > 0}
      host={queue[0] ?? ''}
      onSaved={handleSaved}
      onClose={handleCancel}
    />
  )

  return { preflight, dialog, preflightBusy: busy }
}
