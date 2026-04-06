import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router'
import { getNamespaces, getQuickStarts, deleteNamespace, postNamespaceStart } from '../lib/api'
import type { NamespaceSummaryDto, QuickStartDto } from '../lib/types'
import { ConfirmModal } from '../components/ConfirmModal'
import { ContextMenu } from '../components/ContextMenu'
import type { ContextMenuItem } from '../components/ContextMenu'
import { useContextMenu } from '../hooks/useContextMenu'
import { useTabsStore } from '../lib/tabs'
import { useDashboardStore } from '../lib/store'
import { usePanelStore } from '../lib/panels'
import { useTranslation } from '../lib/i18n'
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
    openTab({ id: 'wizard', title: t('welcome.createNew'), path: '/wizard' })
    navigate('/wizard')
  }

  function nsContextItems(ns: NamespaceSummaryDto): ContextMenuItem[] {
    return [
      { label: t('welcome.context.open'), onClick: () => handleOpenNamespace(ns) },
      { label: t('welcome.context.delete'), variant: 'danger', onClick: () => setDeleteTarget(ns) },
    ]
  }

  return (
    <div className="flex flex-col items-center justify-center min-h-full p-8">
      {/* Title */}
      <h1 className="text-3xl font-bold text-foreground mb-12">{t('welcome.title')}</h1>

      {/* Namespace buttons */}
      <div className="w-full max-w-md flex flex-col gap-3">
        {loading ? (
          <div className="text-center text-muted-foreground text-sm py-4">{t('welcome.loading')}</div>
        ) : loadError ? (
          <div className="text-center text-destructive text-sm py-4">{t('welcome.error', { error: loadError })}</div>
        ) : (
          <>
            {startError && (
              <div className="text-center text-destructive text-sm py-2 mb-2">{t('welcome.startFailed', { error: startError })}</div>
            )}
            {namespaces.map((ns) => (
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

            {/* Quick starts as additional namespace options */}
            {quickStarts.length > 0 && namespaces.length === 0 && (
              <button
                type="button"
                onClick={handleCreateNew}
                className="w-full rounded-lg bg-muted hover:bg-muted/70 px-6 py-3.5 text-center transition-colors"
              >
                <div className="text-sm font-semibold text-foreground">{t('welcome.quickStart')}</div>
                <div className="text-xs text-muted-foreground mt-0.5">{quickStarts[0]?.name}</div>
              </button>
            )}

            {/* More button (if multiple quick starts) */}
            {quickStarts.length > 1 && (
              <button
                type="button"
                onClick={handleCreateNew}
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
    </div>
  )
}
