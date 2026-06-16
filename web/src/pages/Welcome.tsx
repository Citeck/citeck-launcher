import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { ApiError, activateNamespace, getNamespaces, getQuickStarts, deleteNamespace, postNamespaceStart, createNamespace, closeAllDesktopWindows } from '../lib/api'
import { useDaemonStatusStore, useActiveWorkspaceId } from '../lib/daemonStatus'
import type { NamespaceSummaryDto, QuickStartDto } from '../lib/types'
import { ConfirmModal } from '../components/ConfirmModal'
import { NamespaceDialog } from '../components/NamespaceDialog'
import { NamespaceEditDialog } from '../components/NamespaceEditDialog'
import { LoadingHint } from '../components/LoadingHint'
import { GitPullErrorDialog, type GitPullDecision } from '../components/GitPullErrorDialog'
import { useRegistryPreflight } from '../components/RegistryPreflight'
import { ContextMenu } from '../components/ContextMenu'
import type { ContextMenuItem } from '../components/ContextMenu'
import { useContextMenu } from '../hooks/useContextMenu'
import { useTabsStore } from '../lib/tabs'
import { useDashboardStore } from '../lib/store'
import { useQuickStartStore } from '../lib/quickstart'
import { StartProgressStepper } from '../components/StartProgressStepper'
import { usePanelStore } from '../lib/panels'
import { useTranslation } from '../lib/i18n'
import { showError } from '../lib/errorModal'
import { MoreHorizontal, Plus, Loader2 } from 'lucide-react'

export function Welcome() {
  const { t } = useTranslation()
  const [namespaces, setNamespaces] = useState<NamespaceSummaryDto[]>([])
  const [quickStarts, setQuickStarts] = useState<QuickStartDto[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<NamespaceSummaryDto | null>(null)
  const [deleteLoading, setDeleteLoading] = useState(false)
  const [deleteError, setDeleteError] = useState('')
  const [starting, setStarting] = useState(false)
  const [startError, setStartError] = useState<string | null>(null)
  const [moreOpen, setMoreOpen] = useState(false)
  // One form for both create and edit (NamespaceEditDialog re-initializes on
  // every open via its [open, mode, initial] effect, so a single instance is
  // enough — no separate create/edit dialogs). null = closed.
  const [nsForm, setNsForm] = useState<{ mode: 'create' | 'edit'; target?: NamespaceSummaryDto } | null>(null)
  // The git-pull-error dialog derives its `open` from loadError/startError.
  // Cancel/Skip must actually close it, so we remember the exact message the
  // user dismissed — a NEW error (different message) re-opens the dialog.
  const [dismissedGitError, setDismissedGitError] = useState<string | null>(null)
  // Machine-readable code of the most recent loadError/startError (ApiError
  // .code) — lets the git-pull dialog open on the explicit
  // WS_REPO_SYNC_FAILED contract code, not just the message heuristic.
  const [errorCode, setErrorCode] = useState('')
  // Active workspace ID (used for filtering namespaces + scoping the create
  // request explicitly). Sourced from the daemon-status store so a workspace
  // switch from the top-panel picker re-renders + reloads this screen.
  const activeWorkspaceId = useActiveWorkspaceId()
  const workspaceLoaded = useDaemonStatusStore((s) => s.status !== null)
  // Pre-start registry-credentials gate (hard block — see useRegistryPreflight).
  const { preflight, dialog: registryPreflightDialog } = useRegistryPreflight()
  // Kotlin parity (WelcomeScreen.kt:281) — guard MessageDialog when QS clicked
  // but the workspace already has namespaces. Tracked as a transient flag.
  const navigate = useNavigate()
  const openTab = useTabsStore((s) => s.openTab)
  const fetchData = useDashboardStore((s) => s.fetchData)
  const startEventStream = useDashboardStore((s) => s.startEventStream)
  const resetPanels = usePanelStore((s) => s.resetPanels)
  // Quick-start bootstrap phase — module-level store so the progress stepper
  // survives the Welcome remount caused by navigate('/welcome') below and any
  // user navigation during the ~10-minute bootstrap.
  const qsActive = useQuickStartStore((s) => s.active)
  // Quick-start create/start failure — kept in the store (not component
  // state) because handleQuickStart's navigate('/welcome') remounts Welcome
  // and would lose a plain setStartError. Feeds the same GitPullErrorDialog
  // gate as loadError/startError below.
  const qsError = useQuickStartStore((s) => s.error)
  const qsErrorCode = useQuickStartStore((s) => s.errorCode)
  const { contextMenu, showContextMenu, hideContextMenu } = useContextMenu()

  // Holds the latest loadData so the silent-retry timeout can call it without
  // referencing the callback before it's declared (temporal dead zone) and
  // without re-creating loadData on every render.
  const loadDataRef = useRef<() => void>(undefined)

  const loadData = useCallback(async () => {
    setLoading(true)
    try {
      const [ns, qs] = await Promise.all([getNamespaces(), getQuickStarts()])
      setNamespaces(ns)
      setQuickStarts(qs)
      setLoadError(null)
      setErrorCode('')
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      // Daemon still starting — retry silently. WS_REPO_SYNC_FAILED (502) is
      // a real failure (workspace repo unsyncable, no cache) and must surface
      // — it intentionally does NOT match these transient patterns.
      if (msg.includes('Service Unavailable') || msg.includes('503') || msg.includes('Failed to fetch')) {
        setTimeout(() => loadDataRef.current?.(), 1000)
        return
      }
      setLoadError(msg)
      setErrorCode(e instanceof ApiError ? e.code : '')
    } finally {
      setLoading(false)
    }
  }, [])

  // Keep the ref pointing at the latest loadData (stable here, but updated via
  // an effect to avoid mutating a ref during render).
  useEffect(() => {
    loadDataRef.current = loadData
  }, [loadData])

  // Reload the namespace list on mount and whenever the active workspace
  // changes (e.g. the user switches workspace from the top-panel picker).
  useEffect(() => {
    // Intentional: load-on-mount / on-workspace-change fetch sets a loading
    // flag then awaits; not a cascading render.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    loadData()
  }, [loadData, activeWorkspaceId])

  // Ensure daemon status is loaded so the active workspace id resolves. Fails
  // silently — the screen still renders without it.
  useEffect(() => {
    void useDaemonStatusStore.getState().fetch()
  }, [])

  // Arriving at Welcome means we left the active namespace — close any secondary
  // windows (logs / editor) that were tied to it. Kotlin parity:
  // WorkspaceServices.setSelectedNamespace → CiteckWindow.closeAll(). Desktop
  // only; no-ops in the browser. Runs once on mount (you can't open secondary
  // windows from Welcome, so there's nothing to clobber).
  useEffect(() => {
    void closeAllDesktopWindows()
  }, [])

  async function handleOpenNamespace(ns: NamespaceSummaryDto) {
    // Activate the clicked namespace on the daemon FIRST — the daemon's
    // SelectedNs[wsID] only gets set inside installLoadedNamespace, so
    // without this call the choice never persists and the next reload
    // bounces straight back to Welcome. The namespace stays STOPPED until
    // the user clicks Start (auto-start on click is reserved for Quick
    // Start, which creates the namespace from scratch).
    setStartError(null)
    setStarting(true)
    try {
      await activateNamespace(ns.id)
      await fetchData()
      startEventStream()
      resetPanels()
      openTab({ id: 'home', title: t('dashboard.title'), path: '/' })
      navigate('/')
    } catch (err) {
      setStartError(err instanceof Error ? err.message : String(err))
      setErrorCode(err instanceof ApiError ? err.code : '')
    } finally {
      setStarting(false)
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return
    setDeleteLoading(true)
    setDeleteError('')
    try {
      await deleteNamespace(deleteTarget.id)
      setDeleteTarget(null)
      loadData()
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : String(err))
    } finally {
      setDeleteLoading(false)
    }
  }

  function handleCreateNew() {
    setNsForm({ mode: 'create' })
  }

  async function afterCreated() {
    await loadData()
    await fetchData()
    startEventStream()
    resetPanels()
    openTab({ id: 'home', title: t('dashboard.title'), path: '/' })
    navigate('/')
  }

  // Kotlin parity (WelcomeScreen.kt prepareNsDataToCreate): each quickstart
  // variant maps to a NamespaceConfig pre-filled from the template; the daemon
  // already overlays template fields on top of NamespaceCreateDto, so we only
  // need to pass {name, template, snapshot} and the form defaults handle the rest.
  /** Open the namespace dashboard (same outcome as "Open panel" /
   *  handleOpenNamespace) and dismiss the quick-start stepper. Used both by the
   *  stepper's button and automatically once the start has been issued. */
  function openDashboard() {
    useQuickStartStore.getState().dismiss()
    resetPanels()
    openTab({ id: 'home', title: t('dashboard.title'), path: '/' })
    navigate('/')
  }

  function handleQuickStart(qs: QuickStartDto | null) {
    void doQuickStart(qs)
  }

  async function doQuickStart(qs: QuickStartDto | null) {
    // QS buttons are only rendered when the workspace has no namespaces yet
    // (see the render gate below) so this handler runs only for the empty-
    // workspace bootstrap path.
    const qsStore = useQuickStartStore.getState()
    qsStore.begin()
    setStartError(null)
    // Pin Welcome to its explicit route: the index route flips to Dashboard
    // as soon as the dashboard store sees the new namespace, which would
    // unmount the stepper mid-bootstrap. /welcome keeps it visible while
    // still letting the user navigate away (no blocking overlay).
    navigate('/welcome', { replace: true })
    // SSE stream feeds the stepper (app/namespace statuses + pull progress).
    startEventStream()
    try {
      // Kotlin parity (WelcomeScreen.prepareNsDataToCreate): the server
      // copies the namespaceTemplate into the new config and overrides only
      // name/template/snapshot. We do not send authType/host/port/TLS/pgAdmin
      // so the template defaults survive.
      await createNamespace({
        // Fixed namespace name (Kotlin parity: WelcomeScreen.kt:283
        // `.withName("Citeck Default")`). qs.name is the BUTTON label
        // ("Quick Start With Demo Data") — it must not leak into the
        // created namespace's name.
        name: 'Citeck Default',
        template: (qs && qs.template) || '',
        snapshot: (qs && qs.snapshot) || '',
        bundleRepo: '',
        bundleKey: '',
        authType: '',
        host: '',
        port: 0,
        tlsEnabled: false,
        pgAdminEnabled: false,
        workspaceId: activeWorkspaceId || undefined,
        useDefaultPassword: true,
      })
      // Repo clone + bundle resolution + generation finished — the stepper's
      // "bundle" stage completes once the namespace lands in the store.
      useQuickStartStore.getState().created()
    } catch (e) {
      reportQuickStartError(e)
      return
    }
    // The namespace (and its app images) now exists — gate the START on its
    // ACTUAL image registries so we don't start only to stall mid-pull on a
    // 401. Cancelling leaves it created-but-stopped and drops the stepper.
    await preflight(finishQuickStart, abortQuickStart)
  }

  async function finishQuickStart() {
    try {
      // Same start as the dashboard's "Update & Start" (NamespaceControls →
      // postNamespaceStart(false)); the view must not change how it loads.
      await postNamespaceStart()
      // Pull the fresh namespace into the dashboard store, then open its panel
      // — identical outcome to "Open panel" (handleOpenNamespace). The panel
      // shows the start progress via the same SSE stream; no Welcome stepper to
      // get stranded on (its "back" landed on a Welcome that didn't list the
      // new namespace).
      await fetchData()
      openDashboard()
    } catch (e) {
      reportQuickStartError(e)
    }
  }

  function abortQuickStart() {
    // User cancelled the credential prompt: the namespace is created but not
    // started. Drop the stepper and refresh so Welcome shows it (stopped) —
    // the user can set credentials and start it from the dashboard.
    useQuickStartStore.getState().dismiss()
    loadData()
  }

  function reportQuickStartError(e: unknown) {
    const err = e instanceof Error ? e : new Error(String(e))
    // The code rides along in the store (not component state) for the same
    // remount reason as the message itself — see qsError above.
    useQuickStartStore.getState().fail(err.message, e instanceof ApiError ? e.code : '')
    setStartError(err.message)
    showError({
      title: t('welcome.quickStart'),
      message: t('welcome.startFailed', { error: err.message }),
      details: err.stack,
    })
  }

  function nsContextItems(ns: NamespaceSummaryDto): ContextMenuItem[] {
    return [
      { label: t('welcome.context.open'), onClick: () => handleOpenNamespace(ns) },
      { label: t('welcome.namespace.edit'), onClick: () => setNsForm({ mode: 'edit', target: ns }) },
      { label: t('welcome.context.delete'), variant: 'danger', onClick: () => setDeleteTarget(ns) },
    ]
  }

  return (
    <div className="relative flex flex-col items-center justify-center min-h-full p-8">
      {/* Title */}
      <h1 className="text-3xl font-bold text-foreground mb-12">{t('welcome.title')}</h1>

      {/* Namespace buttons */}
      <div className="w-full max-w-md flex flex-col gap-3">
        {qsActive ? (
          // Quick-start bootstrap in progress — compact progress stepper fed
          // by the SSE-driven dashboard store replaces the namespace/QS
          // buttons (clicking them mid-bootstrap makes no sense).
          <StartProgressStepper onOpenDashboard={openDashboard} />
        ) : loading ? (
          <>
            <div className="text-center text-muted-foreground text-sm py-4">{t('welcome.loading')}</div>
            <div className="flex justify-center"><LoadingHint active={loading} /></div>
          </>
        ) : loadError ? (
          <div className="text-center text-destructive text-sm py-4">{t('welcome.error', { error: loadError })}</div>
        ) : workspaceLoaded && !activeWorkspaceId ? (
          // Kotlin parity (WelcomeScreen.kt:101 — `workspaceServices == null`):
          // the central column collapses to a single "Workspace Is Empty" hint
          // rather than rendering the namespace/QS buttons against no workspace.
          <div className="text-center text-foreground text-base py-10">{t('welcome.workspace.empty')}</div>
        ) : (
          <>
            {startError && (
              <div className="text-center text-destructive text-sm py-2 mb-2">{t('welcome.startFailed', { error: startError })}</div>
            )}
            {namespaces
              .filter((ns) => !activeWorkspaceId || !ns.workspaceId || ns.workspaceId === activeWorkspaceId)
              .slice(0, 3)
              .map((ns) => (
              <div key={`${ns.workspaceId}:${ns.id}`} className="relative">
                <button
                  type="button"
                  disabled={starting}
                  onClick={() => handleOpenNamespace(ns)}
                  className="w-full rounded-lg bg-muted hover:bg-muted/70 px-6 py-3.5 text-center transition-colors disabled:opacity-50"
                >
                  <div className="text-sm font-semibold text-foreground">{ns.name || ns.id}</div>
                  {ns.bundleRef && (
                    <div className="text-xs text-muted-foreground mt-0.5">{ns.bundleRef}</div>
                  )}
                </button>
                {/* Context menu trigger */}
                <button
                  type="button"
                  className="absolute right-3 top-1/2 -translate-y-1/2 p-1.5 rounded-full hover:bg-background/50 text-muted-foreground"
                  onClick={(e) => showContextMenu(e, nsContextItems(ns))}
                >
                  <MoreHorizontal size={16} />
                </button>
              </div>
            ))}

            {/* Quick starts — first variant is the big primary button;
                secondary variants render as a row of small buttons. Each
                variant creates a namespace directly (no wizard detour) using
                its template + snapshot.

                Quick Start buttons are hidden as soon as the workspace has
                any namespace — they are an empty-workspace bootstrap path,
                not an ongoing "create another" entry point. When the
                workspace defines no variants at all, fall back to a single
                generic "Quick Start" button (Kotlin parity:
                WelcomeScreen.kt:130-132). */}
            {(() => {
              const visibleNs = namespaces.filter(
                (ns) => !activeWorkspaceId || !ns.workspaceId || ns.workspaceId === activeWorkspaceId,
              )
              if (visibleNs.length > 0) return null
              const showFallback = quickStarts.length === 0
              if (quickStarts.length === 0 && !showFallback) return null
              const list: QuickStartDto[] = quickStarts.length > 0
                ? quickStarts
                : [{ name: t('welcome.quickStart.default'), template: '' }]
              const primary = list[0]
              return (
                <>
                  <button
                    type="button"
                    disabled={starting}
                    onClick={() => handleQuickStart(quickStarts.length === 0 ? null : primary)}
                    className="w-full rounded-lg bg-muted hover:bg-muted/70 px-6 py-3.5 text-center transition-colors disabled:opacity-50"
                  >
                    <div className="text-sm font-semibold text-foreground">{primary.name || t('welcome.quickStart')}</div>
                    {(primary.bundleRef || primary.template) && (
                      <div className="text-xs text-muted-foreground mt-0.5">{primary.bundleRef || primary.template}</div>
                    )}
                  </button>
                  {list.length > 1 && (
                    <div className="flex gap-2 flex-wrap">
                      {list.slice(1).map((qs, i) => (
                        <button
                          key={qs.name || `qs-${i}`}
                          type="button"
                          disabled={starting}
                          onClick={() => handleQuickStart(qs)}
                          className="flex-1 min-w-0 rounded-lg bg-muted hover:bg-muted/70 px-4 py-2 text-center text-xs font-medium text-foreground transition-colors disabled:opacity-50"
                          title={qs.bundleRef || qs.template}
                        >
                          {qs.name || `${t('welcome.quickStart')} ${i + 2}`}
                        </button>
                      ))}
                    </div>
                  )}
                </>
              )
            })()}

            {/* "More" opens NamespaceDialog — the namespace LIST. Show it only
                when the inline list (capped at 3 above) actually hides some:
                i.e. more than 3 namespaces exist. With 0 namespaces there is
                nothing to list, so the button must not appear regardless of how
                many quick-start variants there are. */}
            {(() => {
              const visibleNs = namespaces.filter((ns) =>
                !activeWorkspaceId || !ns.workspaceId || ns.workspaceId === activeWorkspaceId,
              )
              return visibleNs.length > 3
            })() && (
              <button
                type="button"
                onClick={() => setMoreOpen(true)}
                className="w-full rounded-lg bg-muted hover:bg-muted/70 px-6 py-3 text-center text-sm font-medium text-foreground transition-colors"
              >
                {t('welcome.more')}
              </button>
            )}

            {/* Create New Namespace */}
            <button
              type="button"
              onClick={handleCreateNew}
              className="w-full rounded-lg bg-muted hover:bg-muted/70 px-6 py-3 text-center transition-colors flex items-center justify-center gap-2"
            >
              <Plus size={16} className="text-foreground" />
              <span className="text-sm font-medium text-foreground">{t('welcome.createNew')}</span>
            </button>
          </>
        )}
      </div>

      {/* Context menu */}
      {contextMenu && (
        <ContextMenu
          items={contextMenu.items}
          position={contextMenu.position}
          onClose={hideContextMenu}
        />
      )}

      {/* Opening a namespace activates it on the daemon + refetches state,
          which can take a moment. Show a blocking loader modal so the click
          gives immediate feedback instead of a frozen-looking Welcome page. */}
      {starting && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
          <div className="flex flex-col items-center gap-3 rounded-lg border border-border bg-card px-10 py-7 shadow-xl">
            <Loader2 className="h-7 w-7 animate-spin text-primary" />
            <span className="text-sm text-foreground">{t('welcome.opening')}</span>
          </div>
        </div>
      )}

      <ConfirmModal
        open={!!deleteTarget}
        title={t('welcome.delete.title')}
        message={t('welcome.delete.message', { name: deleteTarget?.name || deleteTarget?.id || '' })}
        confirmLabel={t('common.delete')}
        confirmVariant="danger"
        loading={deleteLoading}
        error={deleteError}
        onConfirm={handleDelete}
        onCancel={() => { setDeleteTarget(null); setDeleteError('') }}
      />

      <NamespaceDialog open={moreOpen} onClose={() => setMoreOpen(false)} />

      <NamespaceEditDialog
        open={!!nsForm}
        mode={nsForm?.mode ?? 'create'}
        workspaceId={activeWorkspaceId}
        nsId={nsForm?.mode === 'edit' ? nsForm.target?.id : undefined}
        onClose={() => setNsForm(null)}
        onSaved={() => {
          const isCreate = nsForm?.mode === 'create'
          setNsForm(null)
          // Create navigates into the new namespace (afterCreated); edit just
          // refreshes the Welcome list.
          if (isCreate) void afterCreated()
          else void loadData()
        }}
      />

      {/* Footer logos (Kotlin parity: WelcomeScreen.kt BottomStart / BottomEnd).
          Kept muted via opacity-60 so they don't compete with the namespace
          buttons for attention. */}
      <footer className="absolute bottom-3 left-4 right-4 flex items-end justify-between pointer-events-none">
        <img src="/logo/slsoft_full_logo.svg" alt="slsoft" className="h-20" />
        <img src="/logo/citeck_full_logo.svg" alt="Citeck" className="h-10" />
      </footer>

      {/* Surface git pull failures with the dedicated dialog (Kotlin parity) */}
      {(() => {
        const gitError = loadError || startError || qsError
        // Explicit contract code (WS_REPO_SYNC_FAILED: workspace repo can't
        // sync and no cache exists) opens the dialog even if the message
        // wording drifts; the heuristic stays as a fallback for non-coded
        // errors (e.g. namespace bundle repos).
        const gitCode = errorCode || qsErrorCode
        const gitErrorOpen = !!gitError
          && (gitCode === 'WS_REPO_SYNC_FAILED' || isGitPullError(gitError))
          && gitError !== dismissedGitError
        return (
          <GitPullErrorDialog
            open={gitErrorOpen}
            repoUrl={extractRepoUrl(gitError)}
            errorMessage={gitError || ''}
            skipAvailable={false}
            cancelAvailable={true}
            // Auth-shaped failures get an inline token section that saves a
            // GIT_TOKEN secret onto the active workspace before retrying.
            workspaceId={activeWorkspaceId || undefined}
            onDecide={(d: GitPullDecision) => {
              if (d === 'retry') {
                setDismissedGitError(null)
                setLoadError(null)
                setStartError(null)
                setErrorCode('')
                // A failed quick start cannot be "retried" in place — dismiss
                // the stepper so the Quick Start buttons come back.
                if (qsError) useQuickStartStore.getState().dismiss()
                loadData()
                return
              }
              // 'skip' fires postGitSkipPull inside the dialog itself; both
              // 'skip' and 'cancel' close the dialog by remembering the
              // dismissed message (the inline error text stays visible).
              setDismissedGitError(gitError)
            }}
          />
        )
      })()}
      {registryPreflightDialog}
    </div>
  )
}

/** Heuristic: does the error string look like a git pull / clone failure? */
function isGitPullError(msg: string | null): boolean {
  if (!msg) return false
  const m = msg.toLowerCase()
  return (
    m.includes('git pull') ||
    m.includes('clone repo') ||
    m.includes('pull repo') ||
    m.includes('authentication required') ||
    m.includes('repository not found')
  )
}

/** Best-effort extraction of the repo URL from an error message; returns '' if none. */
function extractRepoUrl(msg: string | null): string {
  if (!msg) return ''
  const match = msg.match(/(https?:\/\/[^\s]+\.git)/i) || msg.match(/(git@[^\s]+)/i)
  return match ? match[1] : ''
}
