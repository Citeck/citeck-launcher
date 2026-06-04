import { useEffect, useMemo, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import {
  createNamespace,
  getBundles,
  getNamespaceCreateDefaults,
  getNamespaceEdit,
  getWorkspaceSnapshots,
  postWorkspaceUpdate,
  putNamespaceEdit,
  type NamespaceEditDto,
} from '../lib/api'
import type { BundleInfoDto, NamespaceCreateDto } from '../lib/types'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'
import { Select } from './Select'
import { Modal, ModalField } from './Modal'
import { LoadingLabel } from './LoadingLabel'

export interface NamespaceEditInitial {
  name?: string
  bundleRepo?: string
  bundleKey?: string
  authType?: string
  users?: string[]
}

interface NamespaceEditDialogProps {
  open: boolean
  mode: 'create' | 'edit'
  initial?: NamespaceEditInitial
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
  initial,
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
      const init = initial
      if (init && init.name !== undefined) {
        setName(init.name ?? '')
        setBundleRepo(init.bundleRepo ?? '')
        setBundleKey(init.bundleKey ?? '')
        setAuthType(init.authType || 'KEYCLOAK')
        setAuthUsers((init.users ?? []).join(', '))
      } else {
        getNamespaceEdit()
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
  }, [open, mode, initial])

  const bundleRepoOptions = useMemo(() => {
    const opts = bundles.map((b) => ({ value: b.repo, label: b.repo }))
    // Preserve a current value not present in the dropdown (e.g. a repo that
    // was later removed from workspace-v1.yml) so edits don't silently drop it.
    if (bundleRepo && !opts.some((o) => o.value === bundleRepo)) {
      opts.unshift({ value: bundleRepo, label: bundleRepo })
    }
    return opts
  }, [bundles, bundleRepo])

  const bundleKeyOptions = useMemo(() => {
    const repo = bundles.find((b) => b.repo === bundleRepo)
    const versions = repo?.versions ?? []
    const opts = versions.map((v) => ({ value: v, label: v }))
    if (bundleKey && !opts.some((o) => o.value === bundleKey)) {
      opts.unshift({ value: bundleKey, label: bundleKey })
    }
    return opts
  }, [bundles, bundleRepo, bundleKey])

  // Reset bundleKey when the user picks a different repo so the dependent
  // select doesn't end up with a value that doesn't exist in the new list.
  useEffect(() => {
    if (!bundleRepo) return
    const repo = bundles.find((b) => b.repo === bundleRepo)
    const versions = repo?.versions ?? []
    if (bundleKey && !versions.includes(bundleKey) && versions.length > 0) {
      // Intentional: reset the dependent select to a valid value when the repo
      // changes so it never holds a stale version; not a cascading render.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setBundleKey(versions[0])
    }
  }, [bundleRepo, bundles, bundleKey])

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
        const payload: NamespaceEditDto = {
          name: name.trim(),
          bundleRepo,
          bundleKey,
          authType,
          users: users ?? [],
          // PUT preserves on-disk values for fields outside the form; pass
          // zero/false placeholders so they don't overwrite anything.
          host: '',
          port: 0,
          tlsEnabled: false,
          pgAdminEnabled: false,
        }
        await putNamespaceEdit(payload)
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
          onChange={setBundleRepo}
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
            disabled={bundlesLoading}
            title={t('namespace.form.bundles.refresh')}
            aria-label={t('namespace.form.bundles.refresh')}
            onClick={() => {
              setBundlesLoading(true)
              postWorkspaceUpdate()
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

