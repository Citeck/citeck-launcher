import { useEffect, useState, useCallback } from 'react'
import { Trash2 } from 'lucide-react'
import { getVolumes, deleteVolume } from '../lib/api'
import { JournalDialog, type JournalAction, type JournalColumn, type JournalCustomButton } from './JournalDialog'
import { ConfirmModal } from './ConfirmModal'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'

interface VolumeRow extends Record<string, unknown> {
  name: string
  size: string
}

// Mirrors SnapshotsDialog.formatBytes; kept inline to avoid pulling a shared
// formatter just for two dialogs (Kotlin used ContainerStats.formatBytes).
function formatBytes(bytes?: number): string {
  if (!bytes || bytes <= 0) return '—'
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)} GB`
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)} MB`
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`
  return `${bytes} B`
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
 * Size comes from Docker's /system/df endpoint (desktop mode); server mode bind
 * mounts under volumesDir do not expose size cheaply — those entries show "—".
 * Per-row action: delete (only when namespace is STOPPED).
 * Custom footer buttons:
 *  - "Snapshots" — opens the SnapshotsDialog
 *  - "Delete All" — iterates volumes and removes them (with confirm)
 */
export function VolumesDialog({ open, onClose, onOpenSnapshots, namespaceStopped }: VolumesDialogProps) {
  const { t } = useTranslation()
  const [volumes, setVolumes] = useState<VolumeRow[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [deleteAllOpen, setDeleteAllOpen] = useState(false)
  const [deletingAll, setDeletingAll] = useState(false)

  const reload = useCallback(() => {
    void Promise.resolve().then(() => {
      setError(null)
      setLoading(true)
      return getVolumes()
        .then((vs) => {
          if (!Array.isArray(vs)) {
            // Defensive: a misbehaving daemon shouldn't crash the dialog.
            // Surface the unexpected shape instead of silently rendering empty.
            console.error('[VolumesDialog] /volumes returned non-array:', vs)
            setError(`Unexpected response: ${JSON.stringify(vs)}`)
            setVolumes([])
            return
          }
          console.debug('[VolumesDialog] loaded', vs.length, 'volumes')
          setVolumes(vs.map((v) => ({ name: v.name, size: formatBytes(v.size) })))
        })
        .catch((e) => {
          console.error('[VolumesDialog] getVolumes failed:', e)
          setError((e as Error).message || String(e))
        })
        .finally(() => setLoading(false))
    })
  }, [])

  useEffect(() => {
    if (open) reload()
  }, [open, reload])

  const columns: JournalColumn<VolumeRow>[] = [
    { label: t('volumes.table.name'), key: 'name', width: '70%' },
    { label: t('volumes.table.size'), key: 'size' },
  ]

  const rowActions: JournalAction<VolumeRow>[] = [
    {
      icon: Trash2,
      // When the namespace is running the action is disabled (Docker can't
      // remove a volume attached to a live container). Surface *why* in the
      // tooltip instead of leaving a dead-looking button.
      title: namespaceStopped ? t('volumes.delete.tooltip') : t('volumes.delete.disabledHint'),
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
      // Kotlin parity (NamespaceScreen.kt:362-389): retry up to 100 iterations,
      // re-listing after each pass. Docker may refuse a volume that is still
      // attached to a stopping container; on subsequent passes it is gone.
      let iterations = 100
      let remaining = await getVolumes()
      while (iterations-- > 0 && remaining.length > 0) {
        let progressed = false
        for (const v of remaining) {
          try {
            await deleteVolume(v.name)
            progressed = true
          } catch {
            // ignored: a volume might be transiently busy — caught by the
            // outer loop's re-list + 500ms back-off
          }
        }
        if (!progressed) {
          await new Promise((r) => setTimeout(r, 500))
        }
        remaining = await getVolumes()
      }
      if (remaining.length > 0) {
        throw new Error(`Delete All did not converge: ${remaining.map((v) => v.name).join(', ')}`)
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
        loading={loading}
        hideSearch
      />
      {error && open && (
        // In-dialog banner (z-[60] sits above the JournalDialog backdrop) —
        // previously a fixed bottom-right toast that was easy to miss while
        // the user kept staring at "Нет данных".
        <div className="fixed top-4 left-1/2 -translate-x-1/2 z-[60] rounded border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive shadow-lg max-w-[80vw]">
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
