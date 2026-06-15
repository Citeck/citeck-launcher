import { useEffect, useState, useCallback } from 'react'
import { useNavigate } from 'react-router'
import { Pencil, Trash2 } from 'lucide-react'
import { getNamespaces, deleteNamespace, activateNamespace } from '../lib/api'
import { JournalDialog, type JournalAction, type JournalColumn } from './JournalDialog'
import { ConfirmModal } from './ConfirmModal'
import { NamespaceEditDialog } from './NamespaceEditDialog'
import { useDashboardStore } from '../lib/store'
import { useTabsStore } from '../lib/tabs'
import { usePanelStore } from '../lib/panels' // resetPanels on namespace switch
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'

interface NamespaceRow extends Record<string, unknown> {
  id: string
  name: string
  bundleRef: string
  status: string
  workspaceId: string
}

interface NamespaceDialogProps {
  open: boolean
  onClose: () => void
  /** Optional callback fired AFTER a namespace is opened (e.g. parent closes itself). */
  onOpened?: () => void
}

/**
 * NamespaceDialog is the Web port of the Welcome screen's "More" button
 * (`WelcomeScreen.kt:154-160`) and the Dashboard namespace-header click
 * (`NamespaceScreen.kt:99` → JournalSelectDialog for NamespaceConfig).
 *
 * Picking a row starts the namespace (if not running) and navigates to "/".
 * Per-row actions: Edit (opens NamespaceEditDialog mode=edit) and Delete (with
 * confirm). Footer "Create" opens NamespaceEditDialog mode=create.
 */
export function NamespaceDialog({ open, onClose, onOpened }: NamespaceDialogProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const namespace = useDashboardStore((s) => s.namespace)
  const fetchData = useDashboardStore((s) => s.fetchData)
  const startEventStream = useDashboardStore((s) => s.startEventStream)
  const openTab = useTabsStore((s) => s.openTab)
  const resetPanels = usePanelStore((s) => s.resetPanels)

  const [rows, setRows] = useState<NamespaceRow[]>([])
  const [loading, setLoading] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<NamespaceRow | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [editTarget, setEditTarget] = useState<NamespaceRow | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [switching, setSwitching] = useState(false)

  // Currently active namespace ID — used to mark the row in the list and to
  // refuse switching when the active runtime is not STOPPED.
  const activeNsID = namespace?.id ?? ''
  const activeStatus = namespace?.status ?? ''
  const canSwitch = !activeNsID || activeStatus === 'STOPPED'

  const reload = useCallback(() => {
    void Promise.resolve().then(() => {
      setLoading(true)
      return getNamespaces()
        .then((ns) => setRows(ns.map((n) => ({
          id: n.id,
          name: n.name || n.id,
          bundleRef: n.bundleRef ?? '',
          status: n.status,
          workspaceId: n.workspaceId,
        }))))
        .catch((e) => toast((e as Error).message, 'error'))
        .finally(() => setLoading(false))
    })
  }, [])

  useEffect(() => {
    if (open) reload()
  }, [open, reload])

  const columns: JournalColumn<NamespaceRow>[] = [
    {
      label: t('namespaces.col.name'),
      key: 'name',
      width: '40%',
      // The active namespace is marked by its pre-checked left checkbox
      // (initialSelectedKeys below), so no extra glyph here.
      render: (row) => <span className="truncate">{row.name}</span>,
    },
    { label: t('namespaces.col.bundle'), key: 'bundleRef' },
    {
      label: t('namespaces.col.status'),
      key: 'status',
      width: '15%',
      render: (row) => <span className="text-[10px] uppercase">{row.status}</span>,
    },
  ]

  // Switch the active namespace (within the current workspace) without
  // auto-starting it. Daemon rejects with 409 when the current namespace
  // isn't STOPPED — we still pre-check via canSwitch to give a clearer
  // toast and avoid a server round-trip.
  async function switchToNamespace(row: NamespaceRow) {
    if (row.id === activeNsID) {
      onClose()
      return
    }
    if (!canSwitch) {
      toast(t('namespaces.switch.blocked'), 'error')
      return
    }
    setSwitching(true)
    try {
      await activateNamespace(row.id)
      await fetchData()
      startEventStream()
      resetPanels()
      openTab({ id: 'home', title: t('dashboard.title'), path: '/' })
      navigate('/')
      onClose()
      onOpened?.()
      toast(t('namespaces.switch.success', { name: row.name }), 'success')
    } catch (e) {
      toast((e as Error).message, 'error')
    } finally {
      setSwitching(false)
    }
  }

  // Key (first-column value = name) of the active namespace row, so the dialog
  // can pre-check its left checkbox instead of a separate active-marker glyph.
  const activeKey = rows.find((r) => r.id === activeNsID)?.name

  const rowActions: JournalAction<NamespaceRow>[] = [
    {
      icon: Pencil,
      title: t('namespaces.action.edit'),
      onClick: (row) => setEditTarget(row),
    },
    {
      icon: Trash2,
      title: t('common.delete'),
      variant: 'danger',
      onClick: (row) => setDeleteTarget(row),
    },
  ]

  async function handleDelete() {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      await deleteNamespace(deleteTarget.id)
      toast(t('namespaces.deleted'), 'success')
      setDeleteTarget(null)
      reload()
    } catch (e) {
      toast((e as Error).message, 'error')
    } finally {
      setDeleting(false)
    }
  }

  return (
    <>
      <JournalDialog<NamespaceRow>
        open={open}
        onClose={onClose}
        title={
          canSwitch
            ? t('namespaces.dialog.title')
            : t('namespaces.dialog.title.locked', { status: activeStatus })
        }
        columns={columns}
        data={rows}
        rowActions={rowActions}
        onCreate={() => setCreateOpen(true)}
        closeWhenEmpty={false}
        selectable={canSwitch && !switching}
        initialSelectedKeys={activeKey ? [activeKey] : []}
        onSelect={(sel) => { if (sel.length === 1) void switchToNamespace(sel[0]) }}
        loading={loading}
        hideSearch
      />
      <ConfirmModal
        open={!!deleteTarget}
        title={t('welcome.delete.title')}
        message={t('welcome.delete.message', { name: deleteTarget?.name ?? '' })}
        confirmLabel={t('common.delete')}
        confirmVariant="danger"
        loading={deleting}
        onConfirm={handleDelete}
        onCancel={() => setDeleteTarget(null)}
      />
      <NamespaceEditDialog
        open={createOpen}
        mode="create"
        onClose={() => setCreateOpen(false)}
        onSaved={() => { setCreateOpen(false); reload() }}
      />
      <NamespaceEditDialog
        open={!!editTarget}
        mode="edit"
        nsId={editTarget?.id}
        onClose={() => setEditTarget(null)}
        onSaved={() => { setEditTarget(null); reload() }}
      />
    </>
  )
}
