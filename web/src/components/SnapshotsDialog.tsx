import { useEffect, useState, useCallback, useRef } from 'react'
import { Download, Loader2, Pencil, Trash2, X } from 'lucide-react'
import { getSnapshots, postExportSnapshot, postImportSnapshot, postImportSnapshotByName, getWorkspaceSnapshots, getVolumes, postSnapshotDownload, renameSnapshot, deleteSnapshot, postOpenDir } from '../lib/api'
import type { SnapshotDto } from '../lib/types'
import { ConfirmModal } from './ConfirmModal'
import { FormDialog, type FormFieldSpec } from './FormDialog'
import { SnapshotCreateDialog } from './SnapshotCreateDialog'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'
import { formatDateTime } from '../lib/datetime'
import { showError } from '../lib/errorModal'
import { startLongOp, endLongOp, useLongOpStore } from '../lib/longOp'
import { formatBytes } from '../lib/format'

interface SnapshotRow {
  name: string
  size: string
  /** Pre-formatted "HH:MM dd.MM.yyyy" for namespace rows; '—' for workspace rows. */
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
 * (view/dialog/SnapshotsDialog.kt). Two-section layout mirrors the Kotlin
 * SnapshotsDialog.kt:84-90 split: workspace snapshots (no Created column)
 * above namespace snapshots (with Created column). Workspace section is
 * hidden entirely when empty so a namespace-only namespace doesn't show an
 * empty heading.
 */
export function SnapshotsDialog({ open, onClose, namespaceStopped }: SnapshotsDialogProps) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [nsRows, setNsRows] = useState<SnapshotRow[]>([])
  const [wsRows, setWsRows] = useState<SnapshotRow[]>([])
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState<string | null>(null)
  const [renameTarget, setRenameTarget] = useState<SnapshotRow | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<SnapshotRow | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  // Timestamped default name, snapshotted when the dialog opens (not per render).
  const [createDefaultName, setCreateDefaultName] = useState('')
  const fileInputRef = useRef<HTMLInputElement>(null)
  // Bumped by the dashboard store on every terminal snapshot SSE event so the
  // list reloads once an async export/import actually finishes (the HTTP call
  // returns 202 before the file exists).
  const snapshotCompleted = useLongOpStore((s) => s.completed)
  // Pending import: when set, the import-confirm modal is shown. Confirming
  // runs the import; cancelling clears the state.
  const [pendingImport, setPendingImport] = useState<
    | { kind: 'name'; name: string }
    | { kind: 'file'; file: File }
    | null
  >(null)

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    else if (!open && dialog.open) dialog.close()
  }, [open])

  const reload = useCallback(() => {
    void Promise.resolve().then(() => {
      setLoading(true)
      return Promise.all([
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
              created: formatDateTime(s.createdAt),
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
      }).finally(() => setLoading(false))
    })
  }, [])

  // Reload on open and whenever a snapshot op completes on the backend (the
  // create/import 202 returns before the file is written — reloading only then
  // is why a freshly created snapshot used to appear only after reopening).
  useEffect(() => {
    if (open) reload()
  }, [open, reload, snapshotCompleted])

  async function runWith(key: string, fn: () => Promise<void>) {
    setBusy(key)
    try {
      await fn()
    } catch (e) {
      const err = e as Error
      showError({ title: t('snapshots.dialog.title'), message: err.message, details: err.stack })
    } finally {
      setBusy(null)
    }
  }

  async function beginImport(target: { kind: 'name'; name: string } | { kind: 'file'; file: File }) {
    try {
      const vols = await getVolumes().catch(() => [] as { name: string; path: string }[])
      if (vols.length === 0) {
        await runImport(target)
      } else {
        setPendingImport(target)
      }
    } catch (e) {
      const err = e as Error
      showError({ title: t('snapshots.dialog.title'), message: err.message, details: err.stack })
    }
  }

  async function runImport(target: { kind: 'name'; name: string } | { kind: 'file'; file: File }) {
    const key = target.kind === 'name' ? `import:${target.name}` : 'import'
    startLongOp('snapshot.import', t('longOp.snapshot.import'))
    let started = false
    try {
      await runWith(key, async () => {
        const res = target.kind === 'name'
          ? await postImportSnapshotByName(target.name)
          : await postImportSnapshot(target.file)
        started = true
        toast(res.message, 'success')
        reload()
      })
    } finally {
      // HTTP returned 202 — leave the overlay to the SSE terminal event
      // (`snapshot_complete`/`snapshot_error`, handled in the dashboard
      // store). If the HTTP call itself rejected (validation / network),
      // no background work was scheduled, so clear immediately.
      if (!started) endLongOp()
    }
  }

  async function handleRename(values: Record<string, unknown>) {
    if (!renameTarget) return
    // Strip trailing .zip from user input — Kotlin parity (CreateOrEditSnapshotDialog).
    const newName = String(values.name || '').trim().replace(/\.zip$/, '')
    if (!newName || newName === renameTarget.name.replace(/\.zip$/, '')) {
      setRenameTarget(null)
      return
    }
    startLongOp('snapshot.rename', t('longOp.snapshot.rename'))
    try {
      await runWith(`rename:${renameTarget.name}`, async () => {
        await renameSnapshot(renameTarget.name, newName)
        setRenameTarget(null)
        reload()
      })
    } finally {
      endLongOp()
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return
    startLongOp('snapshot.delete', t('longOp.snapshot.delete'))
    try {
      await runWith(`delete:${deleteTarget.name}`, async () => {
        await deleteSnapshot(deleteTarget.name)
        toast(t('snapshots.deleted'), 'success')
        setDeleteTarget(null)
        reload()
      })
    } finally {
      endLongOp()
    }
  }

  async function handleCreate(name: string, volumes: string[]) {
    startLongOp('snapshot.export', t('longOp.snapshot.export'))
    let started = false
    try {
      await runWith('export', async () => {
        const res = await postExportSnapshot(name || undefined, volumes)
        started = true
        toast(res.message, 'success')
        setCreateOpen(false)
        reload()
      })
    } finally {
      if (!started) endLongOp()
    }
  }

  function handleImportFile(file: File) {
    void beginImport({ kind: 'file', file })
  }

  function importRow(row: SnapshotRow) {
    if (row.scope === 'workspace') {
      startLongOp('snapshot.download', t('longOp.snapshot.download'))
      let started = false
      void (async () => {
        try {
          await runWith(`download:${row.name}`, async () => {
            const res = await postSnapshotDownload(row.url!, row.id!)
            started = true
            toast(res.message, 'success')
            reload()
          })
        } finally {
          if (!started) endLongOp()
        }
      })()
      return
    }
    void beginImport({ kind: 'name', name: row.name })
  }

  // Client-side duplicate-name check. Rename allows the current name so the
  // user can no-op without triggering this validation.
  const nameInvalid = (_: Record<string, unknown>, v: unknown): string =>
    typeof v === 'string' && /^[\w\-.]+$/.test(v) ? '' : t('snapshots.field.name.invalid')

  const renameFields: FormFieldSpec[] = [
    {
      key: 'name',
      label: t('snapshots.field.name'),
      type: 'text',
      required: true,
      defaultValue: renameTarget?.name ?? '',
      validations: [
        nameInvalid,
        (_, v) => {
          const name = String(v || '').trim().replace(/\.zip$/, '')
          const current = renameTarget?.name.replace(/\.zip$/, '') ?? ''
          if (name === current) return ''
          return nsRows.some((s) => s.name.replace(/\.zip$/, '') === name)
            ? t('snapshots.field.name.alreadyExists')
            : ''
        },
      ],
    },
  ]

  return (
    <>
      <dialog
        ref={dialogRef}
        className="z-50 fixed rounded-lg border border-border bg-card p-0 text-foreground shadow-xl"
        style={{
          width: 'min(90vw, 768px)',
          // Auto-grow to content; the body has its own max-height + scroll
          // and the empty-state row has a min-height so short lists don't
          // pad and empty lists don't collapse.
          maxHeight: 'min(80vh, 720px)',
          top: '50%',
          left: '50%',
          transform: 'translate(-50%, -50%)',
          margin: 0,
        }}
        // Ignore a nested dialog's close bubbling up the React fiber tree (the
        // create/rename FormDialog and delete ConfirmModal live inside this
        // one); only this dialog's own close should fire onClose. See Modal.tsx.
        onClose={(e) => { if (e.target === e.currentTarget) onClose() }}
      >
        <div className="flex flex-col">
          <div className="flex items-center justify-between px-4 py-3 border-b border-border shrink-0">
            <h2 className="text-sm font-semibold">{t('snapshots.dialog.title')}</h2>
            <button
              type="button"
              className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted"
              onClick={onClose}
              aria-label={t('common.close')}
              title={t('common.close')}
            >
              <X size={16} />
            </button>
          </div>

          <div className="overflow-auto px-4 py-2" style={{ maxHeight: 'calc(min(80vh, 720px) - 110px)' }}>
            {loading && wsRows.length === 0 && nsRows.length === 0 ? (
              <div className="flex items-center justify-center gap-2 py-8 text-sm text-muted-foreground">
                <Loader2 size={14} className="animate-spin" />
                {t('common.loading')}
              </div>
            ) : (
              // One shared header over both sections (workspace + namespace),
              // with the section titles as in-table group rows — mirrors the
              // single-header app table on the main namespace screen.
              <table className="w-full text-xs border-collapse">
                <thead>
                  <tr className="text-left text-muted-foreground border-b border-border">
                    <th className="py-1.5 pr-4 font-medium">{t('snapshots.col.name')}</th>
                    <th className="py-1.5 pr-4 font-medium w-[15%]">{t('snapshots.col.size')}</th>
                    <th className="py-1.5 pr-4 font-medium w-[25%]">{t('snapshots.col.created')}</th>
                    <th className="py-1.5 pr-0 text-right font-medium w-24">{t('journal.actions')}</th>
                  </tr>
                </thead>
                <tbody>
                  {wsRows.length > 0 && (
                    <>
                      <GroupRow title={t('snapshots.section.workspace')} />
                      {wsRows.map((row, i) => (
                        <SnapshotRowView
                          key={`ws:${row.name}:${i}`}
                          row={row}
                          busy={busy}
                          namespaceStopped={namespaceStopped}
                          onImport={importRow}
                          onRename={setRenameTarget}
                          onDelete={setDeleteTarget}
                        />
                      ))}
                    </>
                  )}
                  <GroupRow title={t('snapshots.section.namespace')} />
                  {nsRows.length === 0 ? (
                    <tr>
                      <td colSpan={4} className="text-center text-muted-foreground" style={{ height: 80 }}>
                        {t('journal.noData')}
                      </td>
                    </tr>
                  ) : (
                    nsRows.map((row, i) => (
                      <SnapshotRowView
                        key={`ns:${row.name}:${i}`}
                        row={row}
                        busy={busy}
                        namespaceStopped={namespaceStopped}
                        onImport={importRow}
                        onRename={setRenameTarget}
                        onDelete={setDeleteTarget}
                      />
                    ))
                  )}
                </tbody>
              </table>
            )}
          </div>

          <div className="flex items-center justify-end px-4 py-3 border-t border-border shrink-0 gap-2 flex-wrap">
            {/* Wrap the disabled-capable buttons in a span carrying the title:
                a native tooltip on a disabled <button> never shows (pointer
                events are suppressed), so the "stop the namespace first" hint
                would be invisible exactly when it's needed. */}
            <span title={!namespaceStopped ? t('snapshots.requireStopped') : undefined}>
              <button
                type="button"
                className="rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-xs font-medium hover:bg-primary/90 disabled:opacity-50"
                disabled={!namespaceStopped || busy === 'export'}
                onClick={() => { setCreateDefaultName(defaultSnapshotName()); setCreateOpen(true) }}
              >
                {busy === 'export' ? t('volumes.snapshots.exporting') : t('snapshots.create')}
              </button>
            </span>
            <span title={!namespaceStopped ? t('snapshots.requireStopped') : undefined}>
              <button
                type="button"
                className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50"
                disabled={!namespaceStopped || busy === 'import'}
                onClick={() => fileInputRef.current?.click()}
              >
                {busy === 'import' ? t('volumes.snapshots.importing') : t('snapshots.importFile')}
              </button>
            </span>
            <button
              type="button"
              className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted"
              onClick={() => runWith('openDir', async () => {
                const res = await postOpenDir('snapshots')
                if (!res.opened && res.path) {
                  toast(res.path, 'success')
                }
              })}
            >
              {t('snapshots.openNsDir')}
            </button>
            <button
              type="button"
              className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted"
              onClick={onClose}
            >
              {t('common.close')}
            </button>
          </div>
        </div>
      </dialog>

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
      <SnapshotCreateDialog
        open={createOpen}
        existingNames={nsRows.map((s) => s.name)}
        defaultName={createDefaultName}
        loading={busy === 'export'}
        onCancel={() => setCreateOpen(false)}
        onCreate={handleCreate}
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
    </>
  )
}

// In-table section divider (workspace vs namespace) under the single shared
// header row.
function GroupRow({ title }: { title: string }) {
  return (
    <tr>
      <td colSpan={4} className="pt-3 pb-1 text-[11px] font-semibold text-muted-foreground uppercase tracking-wide">
        {title}
      </td>
    </tr>
  )
}

interface SnapshotRowProps {
  row: SnapshotRow
  busy: string | null
  namespaceStopped: boolean
  onImport: (row: SnapshotRow) => void
  onRename: (row: SnapshotRow) => void
  onDelete: (row: SnapshotRow) => void
}

function SnapshotRowView({ row, busy, namespaceStopped, onImport, onRename, onDelete }: SnapshotRowProps) {
  const { t } = useTranslation()
  return (
    <tr className="border-b border-border/20 hover:bg-muted/30">
      <td className="py-[3px] pr-4 font-mono">{row.name}</td>
      <td className="py-[3px] pr-4 text-muted-foreground">{row.size}</td>
      <td className="py-[3px] pr-4 text-muted-foreground">{row.created}</td>
      <td className="py-[3px] pr-0 text-right whitespace-nowrap">
        <RowButton
          icon={Download}
          title={!namespaceStopped ? t('snapshots.requireStopped') : t('snapshots.action.import')}
          disabled={!namespaceStopped || busy !== null}
          onClick={() => onImport(row)}
        />
        {row.scope === 'namespace' && (
          <>
            <RowButton
              icon={Pencil}
              title={t('snapshots.action.rename')}
              disabled={busy !== null}
              onClick={() => onRename(row)}
            />
            <RowButton
              icon={Trash2}
              title={t('snapshots.action.delete')}
              disabled={busy !== null}
              variant="danger"
              onClick={() => onDelete(row)}
            />
          </>
        )}
      </td>
    </tr>
  )
}

interface RowButtonProps {
  icon: React.ComponentType<{ size?: number; className?: string }>
  title: string
  disabled?: boolean
  variant?: 'default' | 'danger'
  onClick: () => void
}

function RowButton({ icon: Icon, title, disabled, variant = 'default', onClick }: RowButtonProps) {
  const hover = variant === 'danger' ? 'hover:text-destructive' : 'hover:text-foreground'
  // Title lives on the wrapping span: a native tooltip on a disabled <button>
  // never shows (pointer events suppressed), so the disabled-reason hint would
  // be invisible exactly when it matters.
  return (
    <span title={title} className="inline-block">
      <button
        type="button"
        className={`inline-flex items-center justify-center p-1 rounded text-muted-foreground ${hover} hover:bg-muted disabled:opacity-40 disabled:hover:bg-transparent`}
        disabled={disabled}
        onClick={(e) => { e.stopPropagation(); onClick() }}
      >
        <Icon size={14} />
      </button>
    </span>
  )
}

function defaultSnapshotName(): string {
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  return `snapshot-${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}_${pad(d.getHours())}${pad(d.getMinutes())}`
}
