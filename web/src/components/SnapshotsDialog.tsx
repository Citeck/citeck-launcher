import { useEffect, useState, useCallback, useRef } from 'react'
import { Download, Pencil, Trash2, Loader2 } from 'lucide-react'
import { getSnapshots, postExportSnapshot, postImportSnapshot, postImportSnapshotByName, getWorkspaceSnapshots, getVolumes, postSnapshotDownload, renameSnapshot, deleteSnapshot } from '../lib/api'
import type { SnapshotDto } from '../lib/types'
import { JournalDialog, type JournalAction, type JournalColumn, type JournalCustomButton } from './JournalDialog'
import { ConfirmModal } from './ConfirmModal'
import { FormDialog, type FormFieldSpec } from './FormDialog'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'

interface SnapshotRow extends Record<string, unknown> {
  name: string
  size: string
  created: string
  scope: 'workspace' | 'namespace'
  url?: string
  id?: string
}

interface WsSnapshot {
  id: string
  name: string
  url: string
  size?: string
}

interface SnapshotsDialogProps {
  open: boolean
  onClose: () => void
  /** When false, "Create Snapshot" is disabled (Kotlin parity: only STOPPED ns can snapshot). */
  namespaceStopped: boolean
}

/**
 * SnapshotsDialog is the Web port of Kotlin's SnapshotsDialog
 * (view/dialog/SnapshotsDialog.kt). It is a single modal that lists both
 * workspace-level snapshots (download from URL + SHA-256 verify) and
 * namespace-level snapshots (local .zip in the namespace dir).
 *
 * Rows from both sources are merged into one table with a `scope` discriminator
 * so the row-action set can stay uniform (rename/delete are namespace-only).
 */
export function SnapshotsDialog({ open, onClose, namespaceStopped }: SnapshotsDialogProps) {
  const { t } = useTranslation()
  const [nsRows, setNsRows] = useState<SnapshotRow[]>([])
  const [wsRows, setWsRows] = useState<SnapshotRow[]>([])
  const [busy, setBusy] = useState<string | null>(null) // tracks button state: "export"|"import"|"download:<id>"|...
  const [renameTarget, setRenameTarget] = useState<SnapshotRow | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<SnapshotRow | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)
  // Pending import: when set, the import-confirm modal is shown. Confirming
  // runs the import; cancelling clears the state. Holds either a pre-existing
  // namespace snapshot (by name) or an uploaded file.
  const [pendingImport, setPendingImport] = useState<
    | { kind: 'name'; name: string }
    | { kind: 'file'; file: File }
    | null
  >(null)

  const reload = useCallback(() => {
    Promise.all([
      getSnapshots().catch(() => [] as SnapshotDto[]),
      getWorkspaceSnapshots().catch(() => [] as WsSnapshot[]),
    ]).then(([ns, ws]) => {
      setNsRows(
        ns
          .slice()
          .sort((a, b) => (b.createdAt > a.createdAt ? 1 : -1))
          .map((s) => ({
            name: s.name,
            size: formatBytes(s.size),
            created: new Date(s.createdAt).toLocaleString(),
            scope: 'namespace' as const,
          })),
      )
      setWsRows(
        ws.map((s) => ({
          name: s.name,
          size: s.size ?? '—',
          created: '—',
          scope: 'workspace' as const,
          url: s.url,
          id: s.id,
        })),
      )
    })
  }, [])

  useEffect(() => {
    if (open) reload()
  }, [open, reload])

  const columns: JournalColumn<SnapshotRow>[] = [
    {
      label: t('snapshots.col.name'),
      key: 'name',
      render: (row) => (
        <span className="font-mono">
          {row.name}{' '}
          <span className="text-[10px] text-muted-foreground uppercase">
            {row.scope === 'workspace' ? t('snapshots.scope.workspace') : t('snapshots.scope.namespace')}
          </span>
        </span>
      ),
    },
    { label: t('snapshots.col.size'), key: 'size', width: '20%' },
    { label: t('snapshots.col.created'), key: 'created', width: '25%' },
  ]

  async function runWith(key: string, fn: () => Promise<void>) {
    setBusy(key)
    try {
      await fn()
    } catch (e) {
      toast((e as Error).message, 'error')
    } finally {
      setBusy(null)
    }
  }

  /**
   * Begin an import: if the namespace currently has any volumes on disk, ask
   * the user to confirm (the import will wipe them). If the namespace is
   * already empty (fresh ns, never started) we skip the prompt and call the
   * actual import path directly.
   */
  async function beginImport(target: { kind: 'name'; name: string } | { kind: 'file'; file: File }) {
    try {
      const vols = await getVolumes().catch(() => [] as { name: string; path: string }[])
      if (vols.length === 0) {
        await runImport(target)
      } else {
        setPendingImport(target)
      }
    } catch (e) {
      toast((e as Error).message, 'error')
    }
  }

  async function runImport(target: { kind: 'name'; name: string } | { kind: 'file'; file: File }) {
    const key = target.kind === 'name' ? `import:${target.name}` : 'import'
    await runWith(key, async () => {
      const res = target.kind === 'name'
        ? await postImportSnapshotByName(target.name)
        : await postImportSnapshot(target.file)
      toast(res.message, 'success')
      reload()
    })
  }

  const rowActions: JournalAction<SnapshotRow>[] = [
    {
      icon: Download,
      title: t('snapshots.action.import'),
      enabledIf: () => namespaceStopped,
      onClick: async (row) => {
        if (row.scope === 'workspace') {
          // Workspace snapshots: download from the URL into the namespace
          // snapshots dir. Importing it after download follows the regular
          // namespace-import path (with the same volume-wipe confirmation).
          await runWith(`download:${row.name}`, async () => {
            const res = await postSnapshotDownload(row.url!, row.id!)
            toast(res.message, 'success')
            reload()
          })
          return
        }
        // Namespace snapshot: import the pre-existing .zip from disk after
        // confirming any existing volumes will be wiped.
        await beginImport({ kind: 'name', name: row.name })
      },
    },
    {
      icon: Pencil,
      title: t('snapshots.action.rename'),
      enabledIf: (row) => row.scope === 'namespace',
      onClick: (row) => setRenameTarget(row),
    },
    {
      icon: Trash2,
      title: t('snapshots.action.delete'),
      variant: 'danger',
      enabledIf: (row) => row.scope === 'namespace',
      onClick: (row) => setDeleteTarget(row),
    },
  ]

  const customButtons: JournalCustomButton<SnapshotRow>[] = [
    {
      label: busy === 'export' ? t('volumes.snapshots.exporting') : t('snapshots.create'),
      variant: 'primary',
      loading: true,
      enabledIf: () => namespaceStopped,
      onClick: () => setCreateOpen(true),
    },
    {
      label: busy === 'import' ? t('volumes.snapshots.importing') : t('snapshots.importFile'),
      loading: true,
      enabledIf: () => namespaceStopped,
      onClick: () => fileInputRef.current?.click(),
    },
  ]

  async function handleRename(values: Record<string, unknown>) {
    if (!renameTarget) return
    // Strip trailing .zip from user input — Kotlin parity (CreateOrEditSnapshotDialog).
    const newName = String(values.name || '').trim().replace(/\.zip$/, '')
    if (!newName || newName === renameTarget.name.replace(/\.zip$/, '')) {
      setRenameTarget(null)
      return
    }
    await runWith(`rename:${renameTarget.name}`, async () => {
      await renameSnapshot(renameTarget.name, newName)
      setRenameTarget(null)
      reload()
    })
  }

  async function handleDelete() {
    if (!deleteTarget) return
    await runWith(`delete:${deleteTarget.name}`, async () => {
      await deleteSnapshot(deleteTarget.name)
      toast(t('snapshots.deleted'), 'success')
      setDeleteTarget(null)
      reload()
    })
  }

  async function handleCreate(values: Record<string, unknown>) {
    const name = String(values.name || '').trim().replace(/\.zip$/, '')
    await runWith('export', async () => {
      const res = await postExportSnapshot(name || undefined)
      toast(res.message, 'success')
      setCreateOpen(false)
      reload()
    })
  }

  function handleImportFile(file: File) {
    void beginImport({ kind: 'file', file })
  }

  const renameFields: FormFieldSpec[] = [
    {
      key: 'name',
      label: t('snapshots.field.name'),
      type: 'text',
      required: true,
      defaultValue: renameTarget?.name ?? '',
      validations: [
        (_, v) => (typeof v === 'string' && /^[\w\-.]+$/.test(v) ? '' : t('snapshots.field.name.invalid')),
      ],
    },
  ]

  const createFields: FormFieldSpec[] = [
    {
      key: 'name',
      label: t('snapshots.field.name'),
      type: 'text',
      required: true,
      defaultValue: defaultSnapshotName(),
      placeholder: 'my-snapshot',
      validations: [
        (_, v) => (typeof v === 'string' && /^[\w\-.]+$/.test(v) ? '' : t('snapshots.field.name.invalid')),
      ],
    },
  ]

  return (
    <>
      <JournalDialog<SnapshotRow>
        open={open}
        onClose={onClose}
        title={t('snapshots.dialog.title')}
        columns={columns}
        data={[...wsRows, ...nsRows]}
        rowActions={rowActions}
        customButtons={customButtons}
      />
      <input
        ref={fileInputRef}
        type="file"
        accept=".zip"
        className="hidden"
        onChange={(e) => {
          const f = e.target.files?.[0]
          if (f) void handleImportFile(f)
          e.target.value = ''
        }}
      />
      <FormDialog
        open={!!renameTarget}
        title={t('snapshots.rename.title')}
        fields={renameFields}
        onSubmit={handleRename}
        onCancel={() => setRenameTarget(null)}
        submitLabel={t('common.save')}
      />
      <FormDialog
        open={createOpen}
        title={t('snapshots.create.title')}
        fields={createFields}
        onSubmit={handleCreate}
        onCancel={() => setCreateOpen(false)}
        submitLabel={t('snapshots.create')}
        loading={busy === 'export'}
      />
      <ConfirmModal
        open={!!deleteTarget}
        title={t('snapshots.delete.title', { name: deleteTarget?.name ?? '' })}
        message={t('snapshots.delete.message')}
        confirmLabel={t('common.delete')}
        confirmVariant="danger"
        onConfirm={handleDelete}
        onCancel={() => setDeleteTarget(null)}
      />
      <ConfirmModal
        open={!!pendingImport}
        title={t('snapshots.import.confirm.title')}
        message={t('snapshots.import.confirm.message')}
        confirmLabel={t('snapshots.action.import')}
        confirmVariant="danger"
        onConfirm={async () => {
          const target = pendingImport
          if (!target) return
          setPendingImport(null)
          await runImport(target)
        }}
        onCancel={() => setPendingImport(null)}
      />
      {busy && <Loader2 className="fixed bottom-4 left-4 z-[60] animate-spin text-muted-foreground" size={20} />}
    </>
  )
}

function formatBytes(bytes: number): string {
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)} GB`
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)} MB`
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`
  return `${bytes} B`
}

function defaultSnapshotName(): string {
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  return `snapshot-${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}_${pad(d.getHours())}${pad(d.getMinutes())}`
}
