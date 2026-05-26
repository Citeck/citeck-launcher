import { useEffect, useState, useCallback } from 'react'
import { Trash2 } from 'lucide-react'
import { getVolumes, deleteVolume } from '../lib/api'
import { JournalDialog, type JournalAction, type JournalColumn, type JournalCustomButton } from './JournalDialog'
import { ConfirmModal } from './ConfirmModal'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'

interface VolumeRow extends Record<string, unknown> {
  name: string
  path: string
  size: string
}

interface VolumesDialogProps {
  open: boolean
  onClose: () => void
  /** Called when user clicks "Snapshots" custom button. Parent decides whether to open the SnapshotsDialog. */
  onOpenSnapshots: () => void
  /** STOPPED status disables "Delete All" — Kotlin parity: deletion only when ns stopped. */
  namespaceStopped: boolean
}

/**
 * VolumesDialog is the Web port of Kotlin's "Show And Manage Volumes" affordance
 * (`NamespaceScreen.kt:357` opens a JournalSelectDialog over `VolumeInfo`).
 *
 * Columns: Name + Size (matches Kotlin column widths 200-450dp / 50-100dp).
 * Per-row action: delete (only when namespace is STOPPED).
 * Custom footer buttons:
 *  - "Snapshots" — opens the SnapshotsDialog
 *  - "Delete All" — iterates volumes and removes them (with confirm)
 */
export function VolumesDialog({ open, onClose, onOpenSnapshots, namespaceStopped }: VolumesDialogProps) {
  const { t } = useTranslation()
  const [volumes, setVolumes] = useState<VolumeRow[]>([])
  const [error, setError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [deleteAllOpen, setDeleteAllOpen] = useState(false)
  const [deletingAll, setDeletingAll] = useState(false)

  const reload = useCallback(() => {
    setError(null)
    getVolumes()
      .then((vs) => setVolumes(vs.map((v) => ({ name: v.name, path: v.path, size: '—' }))))
      .catch((e) => setError(e.message))
  }, [])

  useEffect(() => {
    if (open) reload()
  }, [open, reload])

  const columns: JournalColumn<VolumeRow>[] = [
    { label: t('volumes.table.name'), key: 'name', width: '60%' },
    { label: t('volumes.table.path'), key: 'path' },
  ]

  const rowActions: JournalAction<VolumeRow>[] = [
    {
      icon: Trash2,
      title: t('volumes.delete.tooltip'),
      variant: 'danger',
      enabledIf: () => namespaceStopped,
      onClick: (row) => setDeleteTarget(row.name),
    },
  ]

  const customButtons: JournalCustomButton<VolumeRow>[] = [
    {
      label: t('volumes.snapshots'),
      onClick: () => onOpenSnapshots(),
    },
    {
      label: t('volumes.deleteAll'),
      variant: 'danger',
      enabledIf: () => namespaceStopped && volumes.length > 0,
      loading: true,
      onClick: () => { setDeleteAllOpen(true) },
    },
  ]

  async function handleDeleteOne() {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      await deleteVolume(deleteTarget)
      toast(t('volumes.delete.success'), 'success')
      setDeleteTarget(null)
      reload()
    } catch (e) {
      toast((e as Error).message, 'error')
    } finally {
      setDeleting(false)
    }
  }

  async function handleDeleteAll() {
    setDeletingAll(true)
    try {
      // Kotlin iterates up to 100 times. We iterate sequentially with first-error stop.
      for (const v of volumes) {
        await deleteVolume(v.name)
      }
      toast(t('volumes.deleteAll.success'), 'success')
      setDeleteAllOpen(false)
      reload()
    } catch (e) {
      toast((e as Error).message, 'error')
    } finally {
      setDeletingAll(false)
    }
  }

  return (
    <>
      <JournalDialog<VolumeRow>
        open={open}
        onClose={onClose}
        title={t('volumes.dialog.title')}
        columns={columns}
        data={volumes}
        rowActions={rowActions}
        customButtons={customButtons}
      />
      {error && (
        <div className="fixed bottom-4 right-4 z-[60] rounded border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive shadow-lg">
          {error}
        </div>
      )}
      <ConfirmModal
        open={!!deleteTarget}
        title={t('volumes.delete.title', { name: deleteTarget ?? '' })}
        message={t('volumes.delete.message')}
        confirmLabel={t('common.delete')}
        confirmVariant="danger"
        loading={deleting}
        onConfirm={handleDeleteOne}
        onCancel={() => setDeleteTarget(null)}
      />
      <ConfirmModal
        open={deleteAllOpen}
        title={t('volumes.deleteAll.title')}
        message={t('volumes.deleteAll.message')}
        confirmLabel={t('common.delete')}
        confirmVariant="danger"
        loading={deletingAll}
        onConfirm={handleDeleteAll}
        onCancel={() => setDeleteAllOpen(false)}
      />
    </>
  )
}
