import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router'
import { getNamespaces, getQuickStarts, deleteNamespace, postNamespaceStart, createNamespace, getDaemonStatus } from '../lib/api'
import type { NamespaceSummaryDto, QuickStartDto } from '../lib/types'
import { ConfirmModal } from '../components/ConfirmModal'
import { NamespaceDialog } from '../components/NamespaceDialog'
import { NamespaceEditDialog } from '../components/NamespaceEditDialog'
import { LoadingHint } from '../components/LoadingHint'
import { GitPullErrorDialog, type GitPullDecision } from '../components/GitPullErrorDialog'
import { ContextMenu } from '../components/ContextMenu'
import type { ContextMenuItem } from '../components/ContextMenu'
import { useContextMenu } from '../hooks/useContextMenu'
import { WorkspaceSelector } from '../components/WorkspaceSelector'
import { useTabsStore } from '../lib/tabs'
import { useDashboardStore } from '../lib/store'
import { usePanelStore } from '../lib/panels'
import { useTranslation } from '../lib/i18n'
import { showError } from '../lib/errorModal'
import { MoreHorizontal, Plus } from 'lucide-react'

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
  const [createOpen, setCreateOpen] = useState(false)
  const [editTarget, setEditTarget] = useState<NamespaceSummaryDto | null>(null)
  const [gitErrorOpen, setGitErrorOpen] = useState(false)
  // Active workspace ID (used for filtering namespaces + scoping the create
  // request explicitly). Sourced from /daemon/status.workspace, which is the
  // workspace ID — server mode exposes exactly one ID via that field.
  const [activeWorkspaceId, setActiveWorkspaceId] = useState<string>('')
  const [workspaceLoaded, setWorkspaceLoaded] = useState(false)
  // Kotlin parity (WelcomeScreen.kt:281) — guard MessageDialog when QS clicked
  // but the workspace already has namespaces. Tracked as a transient flag.
  const [qsBlockedOpen, setQsBlockedOpen] = useState(false)
  const navigate = useNavigate()
  const openTab = useTabsStore((s) => s.openTab)
  const fetchData = useDashboardStore((s) => s.fetchData)
  const startEventStream = useDashboardStore((s) => s.startEventStream)
  const resetPanels = usePanelStore((s) => s.resetPanels)
  const { contextMenu, showContextMenu, hideContextMenu } = useContextMenu()

  const loadData = useCallback(async () => {
    setLoading(true)
    try {
      const [ns, qs] = await Promise.all([getNamespaces(), getQuickStarts()])
      setNamespaces(ns)
      setQuickStarts(qs)
      setLoadError(null)
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      // Daemon still starting — retry silently
      if (msg.includes('Service Unavailable') || msg.includes('503') || msg.includes('Failed to fetch')) {
        setTimeout(loadData, 1000)
        return
      }
      setLoadError(msg)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadData()
  }, [loadData])

  // Workspace label — fetched once on mount. Fails silently because the
  // welcome screen still renders without it (the header just shows the
  // generic "Workspace" label).
  useEffect(() => {
    getDaemonStatus()
      .then((s) => { setActiveWorkspaceId(s.workspace || ''); setWorkspaceLoaded(true) })
      .catch(() => { setWorkspaceLoaded(true) /* daemon may still be starting */ })
  }, [])

  async function handleOpenNamespace(ns: NamespaceSummaryDto) {
    if (ns.status === 'STOPPED' || ns.status === 'STALLED') {
      // Start the namespace, then navigate
      setStarting(true)
      setStartError(null)
      try {
        await postNamespaceStart()
        await fetchData()
        startEventStream()
      } catch (e) {
        setStarting(false)
        setStartError(e instanceof Error ? e.message : String(e))
        return
      }
      setStarting(false)
    } else {
      await fetchData()
      startEventStream()
    }
    resetPanels()
    openTab({ id: 'home', title: t('dashboard.title'), path: '/' })
    navigate('/')
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
    setCreateOpen(true)
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
  async function handleQuickStart(qs: QuickStartDto | null) {
    // Kotlin parity: QS is disabled when the workspace already has namespaces;
    // clicking a QS button in that state shows MessageDialog rather than
    // silently creating a duplicate namespace.
    const visibleNsExists = namespaces.some(
      (ns) => !activeWorkspaceId || !ns.workspaceId || ns.workspaceId === activeWorkspaceId,
    )
    if (visibleNsExists) {
      setQsBlockedOpen(true)
      return
    }
    setStarting(true)
    setStartError(null)
    try {
      // Kotlin parity (WelcomeScreen.prepareNsDataToCreate): the server
      // copies the namespaceTemplate into the new config and overrides only
      // name/template/snapshot. We do not send authType/host/port/TLS/pgAdmin
      // so the template defaults survive.
      await createNamespace({
        name: (qs && qs.name) || 'Citeck Default',
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
      await postNamespaceStart()
      await fetchData()
      startEventStream()
      resetPanels()
      openTab({ id: 'home', title: t('dashboard.title'), path: '/' })
      navigate('/')
    } catch (e) {
      const err = e instanceof Error ? e : new Error(String(e))
      setStartError(err.message)
      showError({
        title: t('welcome.quickStart'),
        message: t('welcome.startFailed', { error: err.message }),
        details: err.stack,
      })
    } finally {
      setStarting(false)
    }
  }

  function nsContextItems(ns: NamespaceSummaryDto): ContextMenuItem[] {
    return [
      { label: t('welcome.context.open'), onClick: () => handleOpenNamespace(ns) },
      { label: t('welcome.namespace.edit'), onClick: () => setEditTarget(ns) },
      { label: t('welcome.context.delete'), variant: 'danger', onClick: () => setDeleteTarget(ns) },
    ]
  }

  return (
    <div className="relative flex flex-col items-center justify-center min-h-full p-8">
      {/* Workspace selector (Kotlin parity: WelcomeScreen.kt TopStart row).
          The selector owns the active workspace label + per-workspace
          actions (Force Update / Edit / Delete). In server mode the
          /workspaces endpoint returns 404 and the component collapses to
          nothing; the inline fallback label below preserves layout. */}
      <div className="absolute top-3 left-3 flex items-center gap-1">
        <WorkspaceSelector
          activeId={activeWorkspaceId}
          onChanged={() => {
            // After switch / create / delete / force-update: refetch
            // namespaces + active id so the picker and the namespace list
            // reflect the new workspace state.
            loadData()
            getDaemonStatus().then((s) => setActiveWorkspaceId(s.workspace || '')).catch(() => {})
          }}
        />
        {!activeWorkspaceId && (
          <span className="text-xs text-muted-foreground">{t('welcome.workspace.label')}</span>
        )}
      </div>

      {/* Title */}
      <h1 className="text-3xl font-bold text-foreground mb-12">{t('welcome.title')}</h1>

      {/* Namespace buttons */}
      <div className="w-full max-w-md flex flex-col gap-3">
        {loading ? (
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

            {/* Quick starts — Kotlin parity (WelcomeScreen.kt 4.3 case B):
                first variant is the big primary button; secondary variants
                render as a row of small buttons. Each variant creates a
                namespace directly (no wizard detour) using its template +
                snapshot.

                The QS section is always rendered when the workspace exposes
                variants (Kotlin always rendered them). Clicking a QS while
                namespaces already exist shows the MessageDialog guard from
                WelcomeScreen.kt:281 instead of creating a duplicate. The
                empty-namespaces case in Kotlin (WelcomeScreen.kt:130-132)
                also falls back to a single "Quick Start" button when the
                workspace defines no variants. */}
            {(() => {
              const visibleNs = namespaces.filter(
                (ns) => !activeWorkspaceId || !ns.workspaceId || ns.workspaceId === activeWorkspaceId,
              )
              const showFallback = quickStarts.length === 0 && visibleNs.length === 0
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

            {/* "More" — opens NamespaceDialog (Kotlin parity: WelcomeScreen.kt:154). */}
            {(() => {
              const visibleNs = namespaces.filter((ns) =>
                !activeWorkspaceId || !ns.workspaceId || ns.workspaceId === activeWorkspaceId,
              )
              return (visibleNs.length > 3 || (quickStarts.length > 1 && visibleNs.length === 0))
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

      {/* Kotlin parity (WelcomeScreen.kt:281): MessageDialog-equivalent when
          QS is clicked while the workspace already has namespaces. Reused
          ConfirmModal with no cancel + OK confirm — the dialog purely
          informs, no destructive action. */}
      <ConfirmModal
        open={qsBlockedOpen}
        title={t('welcome.quickStart')}
        message={t('welcome.quickStart.alreadyHasNamespaces')}
        confirmLabel={t('common.ok')}
        onConfirm={() => setQsBlockedOpen(false)}
        onCancel={() => setQsBlockedOpen(false)}
      />

      <NamespaceDialog open={moreOpen} onClose={() => setMoreOpen(false)} />

      <NamespaceEditDialog
        open={createOpen}
        mode="create"
        workspaceId={activeWorkspaceId}
        onClose={() => setCreateOpen(false)}
        onSaved={afterCreated}
      />

      <NamespaceEditDialog
        open={!!editTarget}
        mode="edit"
        initial={editTarget ? {
          name: editTarget.name || editTarget.id,
          bundleRepo: editTarget.bundleRef?.split(':')[0] || '',
          bundleKey: editTarget.bundleRef?.split(':').slice(1).join(':') || '',
        } : undefined}
        onClose={() => setEditTarget(null)}
        onSaved={() => { setEditTarget(null); loadData() }}
      />

      {/* Footer logos (Kotlin parity: WelcomeScreen.kt BottomStart / BottomEnd).
          Kept muted via opacity-60 so they don't compete with the namespace
          buttons for attention. */}
      <footer className="absolute bottom-3 left-4 right-4 flex items-end justify-between pointer-events-none">
        <img src="/logo/slsoft_full_logo.svg" alt="slsoft" className="h-20" />
        <img src="/logo/citeck_full_logo.svg" alt="Citeck" className="h-10" />
      </footer>

      {/* Surface git pull failures with the dedicated dialog (Kotlin parity) */}
      <GitPullErrorDialog
        open={gitErrorOpen || isGitPullError(loadError || startError)}
        repoUrl={extractRepoUrl(loadError || startError)}
        errorMessage={loadError || startError || ''}
        skipAvailable={false}
        cancelAvailable={true}
        onDecide={(d: GitPullDecision) => {
          setGitErrorOpen(false)
          if (d === 'retry') {
            setStartError(null); loadData()
          }
          // 'skip' fires postGitSkipPull inside the dialog itself; 'cancel'
          // and 'skip' both clear the local dialog state here.
        }}
      />
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
