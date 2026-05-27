import { useEffect, useState, useCallback } from 'react'
import { useNavigate } from 'react-router'
import { Code2, Pencil, Trash2 } from 'lucide-react'
import { getNamespaces, deleteNamespace, postNamespaceStart } from '../lib/api'
import { JournalDialog, type JournalAction, type JournalColumn } from './JournalDialog'
import { ConfirmModal } from './ConfirmModal'
import { NamespaceEditDialog } from './NamespaceEditDialog'
import { useDashboardStore } from '../lib/store'
import { useTabsStore } from '../lib/tabs'
import { usePanelStore } from '../lib/panels' // openBottomTab for ConfigEditor
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
 * Per-row actions: Edit (opens NamespaceEditDialog mode=edit), edit-raw-YAML
 * (opens the ConfigEditor bottom tab), Delete (with confirm). Footer "Create"
 * opens NamespaceEditDialog mode=create.
 */
export function NamespaceDialog({ open, onClose, onOpened }: NamespaceDialogProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const fetchData = useDashboardStore((s) => s.fetchData)
  const startEventStream = useDashboardStore((s) => s.startEventStream)
  const openTab = useTabsStore((s) => s.openTab)
  const resetPanels = usePanelStore((s) => s.resetPanels)

  const [rows, setRows] = useState<NamespaceRow[]>([])
  const [deleteTarget, setDeleteTarget] = useState<NamespaceRow | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [editTarget, setEditTarget] = useState<NamespaceRow | null>(null)
  const [createOpen, setCreateOpen] = useState(false)

  const reload = useCallback(() => {
    getNamespaces()
      .then((ns) => setRows(ns.map((n) => ({
        id: n.id,
        name: n.name || n.id,
        bundleRef: n.bundleRef ?? '',
        status: n.status,
        workspaceId: n.workspaceId,
      }))))
      .catch((e) => toast((e as Error).message, 'error'))
  }, [])

  useEffect(() => {
    if (open) reload()
  }, [open, reload])

  const columns: JournalColumn<NamespaceRow>[] = [
    { label: t('namespaces.col.name'), key: 'name', width: '40%' },
    { label: t('namespaces.col.bundle'), key: 'bundleRef' },
    {
      label: t('namespaces.col.status'),
      key: 'status',
      width: '15%',
      render: (row) => <span className="text-[10px] uppercase">{row.status}</span>,
    },
  ]

  async function openNamespace(row: NamespaceRow) {
    try {
      if (row.status === 'STOPPED' || row.status === 'STALLED') {
        await postNamespaceStart()
      }
      await fetchData()
      startEventStream()
      resetPanels()
      openTab({ id: 'home', title: t('dashboard.title'), path: '/' })
      navigate('/')
      onClose()
      onOpened?.()
    } catch (e) {
      toast((e as Error).message, 'error')
    }
  }

  const openBottomTab = usePanelStore((s) => s.openBottomTab)

  const rowActions: JournalAction<NamespaceRow>[] = [
    {
      icon: Pencil,
      title: t('namespaces.action.edit'),
      onClick: (row) => setEditTarget(row),
    },
    {
      icon: Code2,
      title: t('nsEdit.editRawYaml'),
      onClick: async (row) => {
        // Switching to the target namespace first ensures the ConfigEditor
        // bottom tab loads the right namespace.yml (active-namespace scoped).
        if (row.status === 'STOPPED' || row.status === 'STALLED') {
          try { await postNamespaceStart() } catch { /* user can retry */ }
        }
        await fetchData()
        startEventStream()
        openBottomTab({ id: 'ns-config', type: 'ns-config', title: t('nsEdit.editRawYaml') })
        navigate('/')
        onClose()
      },
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
        title={t('namespaces.dialog.title')}
        columns={columns}
        data={rows}
        rowActions={rowActions}
        onCreate={() => setCreateOpen(true)}
        closeWhenEmpty={false}
        selectable
        onSelect={(sel) => { if (sel.length === 1) void openNamespace(sel[0]) }}
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
        initial={editTarget ? {
          name: editTarget.name,
          bundleRepo: editTarget.bundleRef?.split(':')[0] || '',
          bundleKey: editTarget.bundleRef?.split(':').slice(1).join(':') || '',
        } : undefined}
        onClose={() => setEditTarget(null)}
        onSaved={() => { setEditTarget(null); reload() }}
      />
    </>
  )
}
