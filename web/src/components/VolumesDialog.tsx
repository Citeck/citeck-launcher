import { useEffect, useState, useCallback } from 'react'
import { Trash2 } from 'lucide-react'
import { getVolumes, deleteVolume, getVolumeSize } from '../lib/api'
import { LoadingLabel } from './LoadingLabel'
import { JournalDialog, type JournalAction, type JournalColumn, type JournalCustomButton } from './JournalDialog'
import { ConfirmModal } from './ConfirmModal'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'

interface VolumeRow extends Record<string, unknown> {
  name: string
}

type SizeState = { status: 'idle' | 'loading' | 'done'; bytes?: number }

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
  // Per-row lazy size state. Computing a volume's size walks it via `du` in a
  // utils container (Docker has no cheap per-volume size API and /system/df is
  // slow), so the list loads without sizes and the user computes a single row's
  // size on demand by clicking "Compute".
  const [rowSizes, setRowSizes] = useState<Record<string, SizeState>>({})

  const reload = useCallback(() => {
    // Volumes changed → drop any computed sizes so a recompute reflects reality.
    setRowSizes({})
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
          setVolumes(vs.map((v) => ({ name: v.name })))
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

  const computeSize = useCallback((name: string) => {
    setRowSizes((s) => ({ ...s, [name]: { status: 'loading' } }))
    getVolumeSize(name)
      .then(({ size }) => {
        setRowSizes((s) => ({ ...s, [name]: { status: 'done', bytes: size >= 0 ? size : undefined } }))
      })
      .catch((e) => {
        setRowSizes((s) => ({ ...s, [name]: { status: 'idle' } }))
        toast((e as Error).message, 'error')
      })
  }, [])

  const columns: JournalColumn<VolumeRow>[] = [
    { label: t('volumes.table.name'), key: 'name', width: '70%' },
    {
      label: t('volumes.table.size'),
      key: 'size',
      render: (row) => {
        const st = rowSizes[row.name]
        if (st?.status === 'done') return <span>{st.bytes != null ? formatBytes(st.bytes) : '—'}</span>
        const isLoading = st?.status === 'loading'
        return (
          <button
            type="button"
            disabled={isLoading}
            onClick={() => computeSize(row.name)}
            className="rounded border border-border px-1.5 py-0.5 text-[11px] text-foreground hover:bg-muted disabled:opacity-70"
          >
            <LoadingLabel loading={isLoading}>{t('volumes.size.compute')}</LoadingLabel>
          </button>
        )
      },
    },
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
    const target = deleteTarget
    setDeleting(true)
    try {
      await deleteVolume(target)
      toast(t('volumes.delete.success'), 'success')
      setDeleteTarget(null)
      // Drop the row immediately. The authoritative reload below can take many
      // seconds (Docker /system/df is slow), and a lingering deleted row invites
      // repeat clicks that used to 500 with "no such volume".
      setVolumes((vs) => vs.filter((v) => v.name !== target))
      reload()
    } catch (e) {
      toast((e as Error).message, 'error')
      setDeleteTarget(null)
      reload() // resync the list in case the volume was already gone
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
