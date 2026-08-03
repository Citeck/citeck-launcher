import { useEffect, useState } from 'react'
import { getRegistryBindings, setRegistryBinding } from '../lib/api'
import { Modal } from './Modal'
import { SecretPicker } from './SecretPicker'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'

interface RegistryCredentialsDialogProps {
  open: boolean
  /** Docker registry hostname (the part before the first '/' in the image URL). */
  host: string
  /** Called only on a successful save (never on cancel). The parent is
   *  responsible for closing the dialog — this lets a save be told apart from
   *  a cancel (onClose). */
  onSaved?: () => void
  /** Called when the dialog is dismissed without saving (cancel / backdrop). */
  onClose: () => void
  /** Opened from the Settings page rather than from a failed pull. The flow is
   *  identical; only the wording changes — "Sign in to…" / "Save & Retry" imply
   *  a stuck pull to retry, which is not the case when the user came here to
   *  review or correct a binding on their own initiative. */
  manage?: boolean
}

/**
 * Registry-credentials prompt, unified with the git auth flow: it reuses the
 * shared SecretPicker (filtered to REGISTRY_AUTH secrets tagged with this host)
 * so the user PICKS an existing credential — entered once, reused across
 * namespaces/workspaces — or adds a new one, instead of re-typing it per host.
 *
 * On save it binds the host to the chosen secret (POST /registry-bindings);
 * the daemon then rebuilds the registry auth cache and retries every
 * pull-failed app, so the stuck pull recovers without a restart.
 */
export function RegistryCredentialsDialog({ open, host, onSaved, onClose, manage }: RegistryCredentialsDialogProps) {
  const { t } = useTranslation()
  const [selection, setSelection] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // The binding as PERSISTED on the daemon — distinct from `selection`, which
  // tracks whatever the user is currently pointing at. "Remove" acts on (and is
  // enabled by) the persisted one: there is nothing to unbind otherwise.
  const [bound, setBound] = useState('')

  // Preselect the secret currently bound to this host so re-opening the dialog
  // shows the active choice. Reset on each open so a stale selection from a
  // previous host can't leak in.
  useEffect(() => {
    if (!open) return
    let cancelled = false
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setSelection('')
    setBound('')
    setError(null)
    getRegistryBindings()
      .then((b) => {
        if (cancelled) return
        setSelection(b[host] ?? '')
        setBound(b[host] ?? '')
      })
      .catch(() => { /* no daemon bindings yet — leave unselected */ })
    return () => { cancelled = true }
  }, [open, host])

  // Bind the host to a secret, then signal success. onSaved is the success
  // signal; the parent closes (and, for the pre-flight gate, advances to the
  // next host or runs the pending start). We deliberately do NOT call onClose
  // here so callers can tell a save apart from a cancel.
  async function bindAndFinish(secretId: string) {
    if (!secretId) return
    setSaving(true)
    setError(null)
    try {
      await setRegistryBinding(host, secretId)
      toast(t('registryCreds.saved'), 'success')
      onSaved?.()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  // Creating a credential for the host we're prompting about means "use this
  // here": bind it immediately, so the user doesn't have to also click Save —
  // and the binding persists even if the dialog churns on create.
  function handleCreated(secretId: string) {
    setSelection(secretId)
    void bindAndFinish(secretId)
  }

  // Drop the host→secret binding. The daemon treats an empty secretId as
  // "unbind" (handleSetRegistryBinding) and rebuilds the auth caches, so the
  // host falls back to scope-matched credentials — or to none at all, which is
  // the point when a wrong credential was bound. Kept separate from
  // bindAndFinish, where an empty id means "nothing picked yet".
  async function removeBinding() {
    if (!bound) return
    setSaving(true)
    setError(null)
    try {
      await setRegistryBinding(host, '')
      setSelection('')
      setBound('')
      toast(t('registryCreds.removed'), 'success')
      onSaved?.()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      open={open}
      title={t(manage ? 'registryCreds.manageTitle' : 'registryCreds.title', { host })}
      onClose={onClose}
      footer={
        <>
          {/* Destructive action pinned left, away from the primary one. Enabled
              only when a binding actually exists — this is the only UI for the
              daemon's unbind path. */}
          <button
            type="button"
            className="rounded-md border border-destructive/40 px-3 py-1.5 text-sm text-destructive hover:bg-destructive/10 disabled:opacity-50"
            onClick={() => void removeBinding()}
            disabled={saving || !bound}
            title={t('registryCreds.removeTooltip', { host })}
          >
            {t('registryCreds.remove')}
          </button>
          <div className="flex items-center gap-2">
            <button
              type="button"
              className="rounded-md border border-border px-3 py-1.5 text-sm hover:bg-muted disabled:opacity-50"
              onClick={onClose}
              disabled={saving}
            >
              {t('common.cancel')}
            </button>
            <button
              type="button"
              className="rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-sm font-medium hover:bg-primary/90 disabled:opacity-50"
              onClick={() => void bindAndFinish(selection)}
              disabled={saving || !selection}
            >
              {t(manage ? 'registryCreds.manageSave' : 'registryCreds.save')}
            </button>
          </div>
        </>
      }
    >
      <p className="text-xs text-muted-foreground">
        {t(manage ? 'registryCreds.manageExplain' : 'registryCreds.explain', { host })}
      </p>
      <SecretPicker
        secretType="REGISTRY_AUTH"
        host={host}
        value={selection}
        onChange={setSelection}
        onCreated={handleCreated}
        defaultNewName={host}
        disabled={saving}
      />
      {error && (
        <div className="rounded-md bg-destructive/10 border border-destructive/20 px-3 py-2 text-xs text-destructive">
          {error}
        </div>
      )}
    </Modal>
  )
}
