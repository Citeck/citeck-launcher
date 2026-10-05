import { useEffect, useMemo, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import {
  createNamespace,
  getBundles,
  getContentStorageOptions,
  getNamespaceCreateDefaults,
  getNamespaceEdit,
  getWorkspaceSnapshots,
  pullBundleRepo,
  putNamespaceEdit,
  type NamespaceEditDto,
} from '../lib/api'
import type { BundleInfoDto, ContentStorageOptionDto, NamespaceCreateDto } from '../lib/types'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'
import { primeDesktopModeCache } from '../lib/desktop'
import {
  CONTENT_STORAGE_DB,
  CONTENT_STORAGE_S3,
  REASON_NO_CONTENT_APP,
  contentStorageTypeOf,
  s3OptionLabel,
  s3UnavailableText,
  type ContentStorageType,
} from '../lib/contentStorage'
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

/**
 * The namespace-config generation that stopped shipping MongoDB. Namespaces
 * created at this generation or later never have the container (only eproc
 * ever used it), so the toggle is offered on OLDER namespaces only — where it
 * is a real choice about a container that is really there.
 */
const MONGO_DROPPED_IN_CONFIG_VERSION = 2

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
  const [isDesktop, setIsDesktop] = useState(false)
  const [tlsEnabled, setTlsEnabled] = useState(false)
  const [mongoEnabled, setMongoEnabled] = useState(false)
  // namespace.yml `apiVersion`. 0 = not loaded yet (create mode, or the GET
  // still in flight) — treated as the CURRENT generation, so a legacy-only
  // toggle never flashes on a form that has no business showing it.
  const [configVersion, setConfigVersion] = useState(0)
  // Content storage. `storedStorage` is what the namespace has today (DB in
  // create mode); `storageChoice` is what the form asks for. S3 is one-way:
  // a namespace stored as S3 shows it read-only, and the daemon refuses
  // S3 → DB (CONTENT_STORAGE_DOWNGRADE) whatever the form sends.
  const [storedStorage, setStoredStorage] = useState<ContentStorageType>(CONTENT_STORAGE_DB)
  const [storageChoice, setStorageChoice] = useState<ContentStorageType>(CONTENT_STORAGE_DB)
  // The namespace keeps its content in an external S3 (the `s3:` block), which
  // S3 content storage cannot be combined with — something only the edit GET
  // knows; the options endpoint answers for the bundle alone.
  const [externalS3, setExternalS3] = useState(false)
  // The bundle the edit GET returned, raw — the daemon's write gate re-asks
  // S3 availability only when the bundle CHANGES, and so does the warning.
  const [loadedBundle, setLoadedBundle] = useState<{ repo: string; key: string } | null>(null)
  // GET /content-storage/options for the selected bundle, tagged with the
  // bundle it answers so a slow reply for a previous selection is never read
  // as the answer for the current one.
  const [s3Options, setS3Options] = useState<{ bundle: string; option: ContentStorageOptionDto } | null>(null)
  const [tab, setTab] = useState<'general' | 'advanced'>('general')

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

    setTab('general')
    setStoredStorage(CONTENT_STORAGE_DB)
    setStorageChoice(CONTENT_STORAGE_DB)
    setExternalS3(false)
    setLoadedBundle(null)
    primeDesktopModeCache().then(setIsDesktop).catch(() => setIsDesktop(false))

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
      setTlsEnabled(false)
      setMongoEnabled(false)
      setConfigVersion(0)
      if (nsId) {
        getNamespaceEdit(nsId)
          .then((n: NamespaceEditDto) => {
            setName(n.name)
            setBundleRepo(n.bundleRepo)
            setBundleKey(n.bundleKey)
            setAuthType(n.authType || 'KEYCLOAK')
            setAuthUsers((n.users ?? []).join(', '))
            setTlsEnabled(!!n.tlsEnabled)
            setMongoEnabled(!!n.mongoEnabled)
            setConfigVersion(n.configVersion ?? 1)
            const stored = contentStorageTypeOf(n.contentStorage)
            setStoredStorage(stored)
            setStorageChoice(stored)
            setExternalS3(!!n.externalS3)
            setLoadedBundle({ repo: n.bundleRepo, key: n.bundleKey })
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
      setTlsEnabled(false)
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

  // Ask which content storage the selected bundle can have, whenever the
  // selection changes — and again after a bundle-list refresh, which is how a
  // bundle that was not on disk yet (BUNDLE_UNRESOLVED) becomes answerable.
  // Best-effort: a failed request leaves the answer unknown, which hides the
  // S3 option (and never offers what saving might refuse).
  useEffect(() => {
    if (!open || !bundleRepo || !bundleKey) return
    const bundle = `${bundleRepo}\n${bundleKey}`
    const ctl = new AbortController()
    getContentStorageOptions(bundleRepo, bundleKey, mode === 'create' ? workspaceId : undefined, ctl.signal)
      .then((o) => setS3Options({ bundle, option: o.s3 }))
      .catch(() => {})
    return () => ctl.abort()
  }, [open, mode, workspaceId, bundleRepo, bundleKey, bundles])

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

  // Legacy-only question — see MONGO_DROPPED_IN_CONFIG_VERSION. configVersion
  // 0 means "create mode, or the GET has not landed yet": hide it, so a toggle
  // that may not apply never flashes into view and back out.
  const showMongoToggle = mode === 'edit'
    && configVersion > 0
    && configVersion < MONGO_DROPPED_IN_CONFIG_VERSION
  // The tab strip appears only when Advanced has something in it; an empty
  // second tab is worse than no tabs.
  const hasAdvanced = showMongoToggle

  // Content storage, derived. The option is offered only once the daemon has
  // answered for THIS bundle: a bundle without ecos-content (every Community
  // bundle) never has the question, so the form stays exactly as it was —
  // unless the namespace already keeps its content in S3, which is always
  // shown (read-only), whatever the bundle says.
  const s3Opt = s3Options && s3Options.bundle === `${bundleRepo}\n${bundleKey}` ? s3Options.option : null
  const providerApp = s3Opt?.providerApp ?? ''
  const s3Label = s3OptionLabel(s3Opt?.providerTitle, providerApp)
  const storedS3 = storedStorage === CONTENT_STORAGE_S3
  const s3Selectable = !storedS3 && !externalS3 && !!s3Opt?.available
  // A choice of S3 that the current bundle cannot have reads — and saves — as
  // DB: the irreversible direction must never be the one a stale click picks.
  const effectiveStorage: ContentStorageType = storedS3 || (storageChoice === CONTENT_STORAGE_S3 && s3Selectable)
    ? CONTENT_STORAGE_S3
    : CONTENT_STORAGE_DB
  const showStorageField = storedS3 || (!!s3Opt && s3Opt.reason !== REASON_NO_CONTENT_APP)
  // An S3 namespace moved to a bundle that cannot have S3 is refused on save
  // (CONTENT_STORAGE_UNAVAILABLE). Say so before the click; the daemon still
  // decides.
  const bundleChanged = loadedBundle !== null
    && (loadedBundle.repo !== bundleRepo || loadedBundle.key !== bundleKey)
  const storedS3Refused = storedS3 && bundleChanged && !!s3Opt && !s3Opt.available

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
    if (!validate()) {
      // Every validated field lives on the General tab; leaving the user on
      // Advanced would show a form that refuses to submit and says nothing.
      setTab('general')
      return
    }
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
          tlsEnabled: isDesktop ? tlsEnabled : false,
          // Only meaningful when tlsEnabled; scope it so the payload's intent is
          // explicit (the create route reads tlsMode only inside if req.TLSEnabled).
          tlsMode: isDesktop && tlsEnabled ? 'self-signed' : undefined,
          pgAdminEnabled: false,
          workspaceId: workspaceId || undefined,
          snapshot: snapshot || undefined,
          useDefaultPassword: true,
          // Always the choice the form shows — DB included. The daemon reads
          // the request alone (a template's contentStorage is ignored), so
          // what was shown is exactly what is created.
          contentStorage: effectiveStorage,
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
          tlsEnabled: isDesktop ? tlsEnabled : undefined,
          // Same "absent = unchanged" contract: only sent while the toggle is
          // actually offered, so saving a namespace that has no MongoDB
          // question cannot write an answer to it.
          mongoEnabled: showMongoToggle ? mongoEnabled : undefined,
          // Absent = unchanged. Only the DB → S3 switch is ever sent: the
          // daemon re-attaches the provider and reloads on exactly that.
          contentStorage: !storedS3 && effectiveStorage === CONTENT_STORAGE_S3 ? CONTENT_STORAGE_S3 : undefined,
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
      {hasAdvanced && (
        <div className="flex gap-1 border-b border-border -mt-1">
          {(['general', 'advanced'] as const).map((id) => (
            <button
              key={id}
              type="button"
              role="tab"
              aria-selected={tab === id}
              className={`px-3 py-1.5 text-xs border-b-2 -mb-px ${
                tab === id
                  ? 'border-primary text-foreground font-medium'
                  : 'border-transparent text-muted-foreground hover:text-foreground'
              }`}
              onClick={() => setTab(id)}
            >
              {t(`nsEdit.tab.${id}`)}
            </button>
          ))}
        </div>
      )}

      {tab === 'advanced' ? (
        <>
          {showMongoToggle && (
            <label className="flex items-start gap-2 text-sm">
              <input
                type="checkbox"
                className="rounded border-border align-middle m-0 mt-0.5 shrink-0"
                checked={mongoEnabled}
                onChange={(e) => setMongoEnabled(e.target.checked)}
              />
              <span className="flex flex-col gap-0.5">
                <span className="text-xs font-medium">{t('nsEdit.mongo.label')}</span>
                <span className="text-[11px] text-muted-foreground">{t('nsEdit.mongo.hint')}</span>
              </span>
            </label>
          )}
        </>
      ) : (
        <>
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
        {/* This is a PICKER over repos already declared in the workspace
            config, not a place to configure one — the near-identical label two
            levels down ("Repository URL" in workspace settings) is where the
            git settings actually live, and users kept hunting for them here. */}
        <p className="mt-1 text-[11px] text-muted-foreground">
          {t('namespace.form.bundlesRepoHint')}
        </p>
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

      {showStorageField && (
        <fieldset className="min-w-0">
          <legend className="block text-xs font-medium mb-1">{t('contentStorage.label')}</legend>
          {storedS3 ? (
            <>
              <div className="rounded border border-border bg-muted/40 px-2.5 py-1.5 text-sm">
                {s3Label}
              </div>
              <p className="mt-1 text-[11px] text-muted-foreground">{t('contentStorage.s3.oneWay')}</p>
              {storedS3Refused && (
                <p className="mt-1 text-[11px] text-destructive">
                  {t('contentStorage.bundleChange.refused', {
                    reason: s3UnavailableText(s3Opt?.reason, providerApp, t),
                  })}
                </p>
              )}
            </>
          ) : (
            <div className="flex flex-col gap-1.5">
              <label className="flex items-start gap-2 text-sm">
                <input
                  type="radio"
                  name="contentStorage"
                  className="m-0 mt-0.5 shrink-0"
                  checked={effectiveStorage === CONTENT_STORAGE_DB}
                  onChange={() => setStorageChoice(CONTENT_STORAGE_DB)}
                />
                <span className="flex flex-col gap-0.5">
                  <span className="text-xs font-medium">{t('contentStorage.option.db')}</span>
                  <span className="text-[11px] text-muted-foreground">{t('contentStorage.option.db.hint')}</span>
                </span>
              </label>
              <label className={`flex items-start gap-2 text-sm ${s3Selectable ? '' : 'cursor-not-allowed'}`}>
                <input
                  type="radio"
                  name="contentStorage"
                  className="m-0 mt-0.5 shrink-0"
                  disabled={!s3Selectable}
                  checked={effectiveStorage === CONTENT_STORAGE_S3}
                  onChange={() => setStorageChoice(CONTENT_STORAGE_S3)}
                />
                <span className="flex flex-col gap-0.5">
                  <span className={`text-xs font-medium ${s3Selectable ? '' : 'text-muted-foreground'}`}>
                    {s3Label}
                  </span>
                  <span className="text-[11px] text-muted-foreground">
                    {s3Selectable
                      ? t('contentStorage.option.s3.hint', { app: providerApp })
                      : externalS3
                        ? t('contentStorage.reason.externalS3')
                        : s3UnavailableText(s3Opt?.reason, providerApp, t)}
                  </span>
                </span>
              </label>
              {effectiveStorage === CONTENT_STORAGE_S3 && (
                <p className="rounded-md border border-amber-500/30 bg-amber-500/10 px-2.5 py-1.5 text-[11px] text-amber-700 dark:text-amber-300">
                  {t(mode === 'create' ? 'contentStorage.s3.note.create' : 'contentStorage.s3.note.switch', { app: providerApp })}
                </p>
              )}
            </div>
          )}
        </fieldset>
      )}

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

      {isDesktop && (
        <div>
          <label className="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              className="rounded border-border align-middle m-0 mt-0.5 shrink-0"
              checked={tlsEnabled}
              onChange={(e) => setTlsEnabled(e.target.checked)}
            />
            <span className="flex flex-col gap-0.5">
              <span className="text-xs font-medium">{t('nsEdit.https.label')}</span>
              <span className="text-[11px] text-muted-foreground">{t('nsEdit.https.hint')}</span>
            </span>
          </label>
        </div>
      )}

        </>
      )}

      {submitError && (
        <div className="rounded-md bg-destructive/10 border border-destructive/20 px-3 py-2 text-xs text-destructive">
          {submitError}
        </div>
      )}
    </Modal>
  )
}

