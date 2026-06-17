import { useEffect, useMemo, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import {
  createNamespace,
  getBundles,
  getNamespaceCreateDefaults,
  getNamespaceEdit,
  getWorkspaceSnapshots,
  pullBundleRepo,
  putNamespaceEdit,
  type NamespaceEditDto,
} from '../lib/api'
import type { BundleInfoDto, NamespaceCreateDto } from '../lib/types'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'
import { Select } from './Select'
import { Modal, ModalField } from './Modal'
import { LoadingLabel } from './LoadingLabel'

interface NamespaceEditDialogProps {
  open: boolean
  mode: 'create' | 'edit'
  /**
   * ID of the namespace being edited — REQUIRED in edit mode. The dialog
   * always loads authoritative values via GET /namespaces/{id}/edit (raw
   * bundleKey: a stored "LATEST" comes back as "LATEST", not the resolved
   * version) and saves via PUT /namespaces/{id}/edit, so editing any listed
   * namespace never patches the active one by accident.
   */
  nsId?: string
  workspaceId?: string
  onClose: () => void
  onSaved?: () => void
}

interface WsSnapshotOpt {
  id: string
  name: string
}

/**
 * Single-modal namespace create/edit form (Kotlin parity:
 * NamespaceEntityDef.formSpec). The 1:1 field set is name, bundlesRepo,
 * bundleKey, snapshot (CREATE only), authType, authUsers (when BASIC).
 * Host/port/TLS/pgAdmin are NOT part of this form — power users edit those
 * via raw YAML (Dashboard cog → "Edit raw YAML").
 */
export function NamespaceEditDialog({
  open,
  mode,
  nsId,
  workspaceId,
  onClose,
  onSaved,
}: NamespaceEditDialogProps) {
  const { t } = useTranslation()

  const [name, setName] = useState('')
  const [bundleRepo, setBundleRepo] = useState('')
  const [bundleKey, setBundleKey] = useState('')
  const [snapshot, setSnapshot] = useState('')
  const [authType, setAuthType] = useState('KEYCLOAK')
  const [authUsers, setAuthUsers] = useState('')

  const [bundles, setBundles] = useState<BundleInfoDto[]>([])
  const [snapshots, setSnapshots] = useState<WsSnapshotOpt[]>([])
  const [bundlesLoading, setBundlesLoading] = useState(false)
  const [loading, setLoading] = useState(false)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})

  useEffect(() => {
    if (!open) {
      // Intentional: reset form error state when the dialog closes so it opens
      // clean next time; not a cascading render.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setSubmitError(null)
      setFieldErrors({})
      return
    }

    setBundlesLoading(true)
    getBundles()
      .then((bs) => setBundles(bs))
      .catch(() => setBundles([]))
      .finally(() => setBundlesLoading(false))

    if (mode === 'create') {
      getWorkspaceSnapshots()
        .then((s) => setSnapshots(s.map((x) => ({ id: x.id, name: x.name }))))
        .catch(() => setSnapshots([]))
    }

    if (mode === 'edit') {
      // Always load authoritative values from the daemon — never seed from a
      // partial caller-supplied object (a partial seed wiped auth back to
      // KEYCLOAK/[] and pinned "LATEST" to the display-resolved version).
      // Blank the form first so a previously edited namespace's values never
      // flash (or get saved) for the newly opened one.
      setName('')
      setBundleRepo('')
      setBundleKey('')
      setAuthType('KEYCLOAK')
      setAuthUsers('')
      if (nsId) {
        getNamespaceEdit(nsId)
          .then((n: NamespaceEditDto) => {
            setName(n.name)
            setBundleRepo(n.bundleRepo)
            setBundleKey(n.bundleKey)
            setAuthType(n.authType || 'KEYCLOAK')
            setAuthUsers((n.users ?? []).join(', '))
          })
          .catch((e) => setSubmitError((e as Error).message))
      }
    } else {
      // mode=create: blank the form, then overlay server-computed defaults
      // (Kotlin 1.x parity — auto "Citeck #N" + template-driven bundle/auth).
      // Failure is non-fatal: the user just sees an empty form, which is the
      // pre-port behavior. The active workspace is implicit on the daemon —
      // it picks defaults from whichever workspace currently owns the socket.
      setName('')
      setBundleRepo('')
      setBundleKey('')
      setSnapshot('')
      setAuthType('')
      setAuthUsers('')
      getNamespaceCreateDefaults()
        .then((d) => {
          setName(d.name)
          setBundleRepo(d.bundleRepo)
          setBundleKey(d.bundleKey)
          setAuthType(d.authType || 'KEYCLOAK')
          setAuthUsers((d.users ?? []).join(', '))
        })
        .catch(() => {
          setAuthType('KEYCLOAK')
        })
    }
  }, [open, mode, nsId])

  const bundleRepoOptions = useMemo(() => {
    // Offer every configured repo, even one with no versions on disk yet (e.g.
    // release / alf-develop, which were never cloned by the active ref). The
    // user selects it and hits the refresh button to force-pull its versions.
    const opts = bundles.map((b) => ({ value: b.repo, label: b.repo }))
    // Preserve a current value not present in the dropdown (e.g. a repo that
    // was later removed from workspace-v1.yml, or whose releases aren't synced
    // yet) so edits don't silently drop it.
    if (bundleRepo && !opts.some((o) => o.value === bundleRepo)) {
      opts.unshift({ value: bundleRepo, label: bundleRepo })
    }
    return opts
  }, [bundles, bundleRepo])

  const bundleKeyOptions = useMemo(() => {
    const repo = bundles.find((b) => b.repo === bundleRepo)
    const versions = repo?.versions ?? []
    // Versions are newest-first — mark the newest with "(LATEST)" instead of
    // offering a separate symbolic "LATEST" entry.
    const latest = versions[0]
    const opts = versions.map((v) => ({
      value: v,
      label: v === latest ? `${v} (${t('namespace.form.bundleKey.latest')})` : v,
    }))
    // Preserve a current CONCRETE pin missing from the list (e.g. an unsynced
    // repo). Symbolic LATEST is normalized to the concrete newest by the effect
    // below, so it never needs an option of its own.
    if (bundleKey && !/^latest$/i.test(bundleKey) && !opts.some((o) => o.value === bundleKey)) {
      opts.unshift({ value: bundleKey, label: bundleKey })
    }
    return opts
  }, [bundles, bundleRepo, bundleKey, t])

  // Keep bundleKey valid + concrete. The UI no longer offers a symbolic
  // "LATEST": normalize it (and any stale pin not in the current repo's list)
  // to the concrete newest version (versions[0]), which the dropdown marks
  // "(LATEST)". This also pins a namespace that was stored as "LATEST" to the
  // concrete latest on the next save — intentional, the symbolic option is gone.
  useEffect(() => {
    if (!bundleRepo) return
    const repo = bundles.find((b) => b.repo === bundleRepo)
    const versions = repo?.versions ?? []
    if (versions.length === 0) return
    // Pick the newest (versions are newest-first) when nothing concrete is
    // selected yet — empty (e.g. right after a pull surfaced this repo's
    // versions), the symbolic "LATEST", or a stale pin not in the list.
    if (!versions.includes(bundleKey) || /^latest$/i.test(bundleKey)) {
      // Intentional: normalize the dependent select to a concrete version; not
      // a cascading render.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setBundleKey(versions[0])
    }
  }, [bundleRepo, bundles, bundleKey])

  // When the user picks a different bundle repo, auto-select that repo's latest
  // release (versions are newest-first). Only fires on a manual repo change —
  // on initial edit-load the repo is set directly (not through this handler),
  // so an existing pinned version is preserved.
  function handleRepoChange(repo: string) {
    setBundleRepo(repo)
    const r = bundles.find((b) => b.repo === repo)
    setBundleKey(r?.versions?.[0] ?? '')
    // Sync this repo on selection (throttled server-side — no background
    // pulling): clones if never fetched, re-pulls once the period elapses,
    // otherwise a no-op. Explicit refresh (↻) forces past the throttle.
    if (repo) {
      setBundlesLoading(true)
      pullBundleRepo(repo)
        .then(() => getBundles())
        .then((bs) => setBundles(bs))
        .catch((e) => toast((e as Error).message, 'error'))
        .finally(() => setBundlesLoading(false))
    }
  }

  function validate(): boolean {
    const errors: Record<string, string> = {}
    if (!name.trim()) errors.name = t('namespace.form.required')
    else if (name.length > 64) errors.name = t('namespace.form.nameTooLong')
    if (!bundleRepo) errors.bundleRepo = t('namespace.form.required')
    if (!bundleKey) errors.bundleKey = t('namespace.form.required')
    if (!authType) errors.authType = t('namespace.form.required')
    if (authType === 'BASIC' && !authUsers.trim()) {
      errors.authUsers = t('namespace.form.required')
    }
    setFieldErrors(errors)
    return Object.keys(errors).length === 0
  }

  async function handleSubmit(e?: React.FormEvent) {
    e?.preventDefault()
    if (!validate()) return
    setLoading(true)
    setSubmitError(null)
    try {
      const users = authType === 'BASIC'
        ? authUsers.split(',').map((u) => u.trim()).filter(Boolean)
        : undefined

      if (mode === 'create') {
        const payload: NamespaceCreateDto = {
          name: name.trim(),
          authType,
          users,
          bundleRepo,
          bundleKey,
          // Server overlays template defaults; we don't override
          // host/port/TLS/pgAdmin from the form (Kotlin parity).
          host: '',
          port: 0,
          tlsEnabled: false,
          pgAdminEnabled: false,
          workspaceId: workspaceId || undefined,
          snapshot: snapshot || undefined,
          useDefaultPassword: true,
        }
        await createNamespace(payload)
        toast(t('namespace.form.createSuccess'), 'success')
      } else {
        if (!nsId) throw new Error('namespace id is missing')
        const payload: NamespaceEditDto = {
          name: name.trim(),
          bundleRepo,
          bundleKey,
          authType,
          // Server contract: absent users = leave stored users unchanged
          // (only BASIC edits the list; KEYCLOAK doesn't use it).
          users,
          // PUT preserves on-disk values for fields outside the form: empty
          // host / zero port mean "leave unchanged", and tlsEnabled /
          // pgAdminEnabled are omitted entirely (absent = leave unchanged —
          // sending false here used to silently disable TLS on save).
          host: '',
          port: 0,
        }
        await putNamespaceEdit(nsId, payload)
        toast(t('nsEdit.saveSuccess'), 'success')
      }
      onSaved?.()
      onClose()
    } catch (err) {
      setSubmitError((err as Error).message)
    } finally {
      setLoading(false)
    }
  }

  const title = mode === 'create' ? t('namespace.dialog.create') : t('namespace.dialog.edit')
  const inputCls = 'w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary'

  return (
    <Modal
      open={open}
      title={title}
      onClose={onClose}
      onSubmit={handleSubmit}
      footer={
        <>
          <button
            type="button"
            className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50"
            onClick={onClose}
            disabled={loading}
          >
            {t('namespace.form.cancel')}
          </button>
          <button
            type="submit"
            className="rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-xs font-medium hover:bg-primary/90 disabled:opacity-50"
            disabled={loading}
          >
            <LoadingLabel loading={loading}>
              {mode === 'create' ? t('namespace.form.submit') : t('namespace.form.save')}
            </LoadingLabel>
          </button>
        </>
      }
    >
      <ModalField label={t('namespace.form.name')} error={fieldErrors.name} required>
        <input
          type="text"
          className={inputCls}
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={64}
          autoFocus
        />
      </ModalField>

      <ModalField label={t('namespace.form.bundlesRepo')} error={fieldErrors.bundleRepo} required>
        <Select
          value={bundleRepo}
          options={bundleRepoOptions}
          onChange={handleRepoChange}
          required
          disabled={bundlesLoading}
          placeholder="—"
        />
      </ModalField>

      <ModalField label={t('namespace.form.bundleKey')} error={fieldErrors.bundleKey} required>
        <div className="flex items-center gap-1.5">
          <div className="flex-1 min-w-0">
            <Select
              value={bundleKey}
              options={bundleKeyOptions}
              onChange={setBundleKey}
              required
              disabled={!bundleRepo || bundlesLoading}
              placeholder="—"
            />
          </div>
          <button
            type="button"
            className="shrink-0 rounded border border-border p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
            disabled={bundlesLoading || !bundleRepo}
            title={t('namespace.form.bundles.refresh')}
            aria-label={t('namespace.form.bundles.refresh')}
            onClick={() => {
              if (!bundleRepo) return
              setBundlesLoading(true)
              // Explicit refresh: force-pull the SELECTED repo (bypass throttle).
              pullBundleRepo(bundleRepo, true)
                .then(() => getBundles())
                .then((bs) => setBundles(bs))
                .catch((e) => toast((e as Error).message, 'error'))
                .finally(() => setBundlesLoading(false))
            }}
          >
            <RefreshCw size={14} className={bundlesLoading ? 'animate-spin' : ''} />
          </button>
        </div>
      </ModalField>

      {mode === 'create' && (
        <ModalField label={t('namespace.form.snapshot')}>
          <Select
            value={snapshot}
            options={snapshots.map((s) => ({ value: s.id, label: s.name }))}
            onChange={setSnapshot}
            placeholder={t('namespace.form.snapshot.none')}
          />
        </ModalField>
      )}

      <ModalField label={t('namespace.form.authType')} error={fieldErrors.authType} required>
        <Select
          value={authType}
          options={[
            { value: 'BASIC', label: 'BASIC' },
            { value: 'KEYCLOAK', label: 'KEYCLOAK' },
          ]}
          onChange={setAuthType}
          required
        />
      </ModalField>

      {authType === 'BASIC' && (
        <ModalField label={t('namespace.form.authUsers')} error={fieldErrors.authUsers} required>
          <input
            type="text"
            className={inputCls}
            value={authUsers}
            onChange={(e) => setAuthUsers(e.target.value)}
            placeholder="admin, user1"
          />
        </ModalField>
      )}

      {submitError && (
        <div className="rounded-md bg-destructive/10 border border-destructive/20 px-3 py-2 text-xs text-destructive">
          {submitError}
        </div>
      )}
    </Modal>
  )
}

