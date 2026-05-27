import { useEffect, useState, useCallback, useRef, useMemo } from 'react'
import { Download, Pencil, Trash2, Loader2, Search, X } from 'lucide-react'
import { getSnapshots, postExportSnapshot, postImportSnapshot, postImportSnapshotByName, getWorkspaceSnapshots, getVolumes, postSnapshotDownload, renameSnapshot, deleteSnapshot, postOpenDir } from '../lib/api'
import type { SnapshotDto } from '../lib/types'
import { ConfirmModal } from './ConfirmModal'
import { FormDialog, type FormFieldSpec } from './FormDialog'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'
import { showError } from '../lib/errorModal'

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
  const [busy, setBusy] = useState<string | null>(null)
  const [renameTarget, setRenameTarget] = useState<SnapshotRow | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<SnapshotRow | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [search, setSearch] = useState('')
  const fileInputRef = useRef<HTMLInputElement>(null)
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
            created: formatSnapshotDate(new Date(s.createdAt)),
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

  const filteredWs = useMemo(() => filterRows(wsRows, search), [wsRows, search])
  const filteredNs = useMemo(() => filterRows(nsRows, search), [nsRows, search])

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
    await runWith(key, async () => {
      const res = target.kind === 'name'
        ? await postImportSnapshotByName(target.name)
        : await postImportSnapshot(target.file)
      toast(res.message, 'success')
      reload()
    })
  }

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

  function importRow(row: SnapshotRow) {
    if (row.scope === 'workspace') {
      void runWith(`download:${row.name}`, async () => {
        const res = await postSnapshotDownload(row.url!, row.id!)
        toast(res.message, 'success')
        reload()
      })
      return
    }
    void beginImport({ kind: 'name', name: row.name })
  }

  // Client-side duplicate-name check. Rename allows the current name so the
  // user can no-op without triggering this validation.
  const nameInvalid = (_: Record<string, unknown>, v: unknown): string =>
    typeof v === 'string' && /^[\w\-.]+$/.test(v) ? '' : t('snapshots.field.name.invalid')

  const createFields: FormFieldSpec[] = [
    {
      key: 'name',
      label: t('snapshots.field.name'),
      type: 'text',
      required: true,
      defaultValue: defaultSnapshotName(),
      placeholder: 'my-snapshot',
      validations: [
        nameInvalid,
        (_, v) => {
          const name = String(v || '').trim().replace(/\.zip$/, '')
          return nsRows.some((s) => s.name.replace(/\.zip$/, '') === name)
            ? t('snapshots.field.name.alreadyExists')
            : ''
        },
      ],
    },
  ]

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
        className="fixed inset-0 z-50 m-auto max-w-3xl w-full max-h-[80vh] rounded-lg border border-border bg-card p-0 text-foreground backdrop:bg-black/50"
        onClose={onClose}
      >
        <div className="flex flex-col h-full max-h-[80vh]">
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

          <div className="px-4 py-2 border-b border-border shrink-0">
            <div className="relative">
              <Search size={14} className="absolute left-2 top-1/2 -translate-y-1/2 text-muted-foreground" />
              <input
                type="text"
                className="w-full rounded border border-border bg-background pl-7 pr-2 py-1 text-xs focus:outline-none focus:border-primary"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={t('journal.filter')}
              />
            </div>
          </div>

          <div className="flex-1 overflow-auto px-4 py-2 space-y-4">
            {filteredWs.length > 0 && (
              <Section title={t('snapshots.section.workspace')}>
                <SnapshotsTable
                  rows={filteredWs}
                  showCreated={false}
                  busy={busy}
                  namespaceStopped={namespaceStopped}
                  onImport={importRow}
                  onRename={(r) => setRenameTarget(r)}
                  onDelete={(r) => setDeleteTarget(r)}
                />
              </Section>
            )}

            <Section title={t('snapshots.section.namespace')}>
              <SnapshotsTable
                rows={filteredNs}
                showCreated
                busy={busy}
                namespaceStopped={namespaceStopped}
                onImport={importRow}
                onRename={(r) => setRenameTarget(r)}
                onDelete={(r) => setDeleteTarget(r)}
              />
            </Section>
          </div>

          <div className="flex items-center justify-end px-4 py-3 border-t border-border shrink-0 gap-2 flex-wrap">
            <button
              type="button"
              className="rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-xs font-medium hover:bg-primary/90 disabled:opacity-50"
              disabled={!namespaceStopped || busy === 'export'}
              onClick={() => setCreateOpen(true)}
            >
              {busy === 'export' ? t('volumes.snapshots.exporting') : t('snapshots.create')}
            </button>
            <button
              type="button"
              className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50"
              disabled={!namespaceStopped || busy === 'import'}
              onClick={() => fileInputRef.current?.click()}
            >
              {busy === 'import' ? t('volumes.snapshots.importing') : t('snapshots.importFile')}
            </button>
            <button
              type="button"
              className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted"
              onClick={() => runWith('openDir', async () => {
                const res = await postOpenDir('volumes')
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

function filterRows(rows: SnapshotRow[], search: string): SnapshotRow[] {
  if (!search.trim()) return rows
  const lower = search.toLowerCase()
  return rows.filter((r) =>
    r.name.toLowerCase().includes(lower) ||
    r.size.toLowerCase().includes(lower) ||
    r.created.toLowerCase().includes(lower),
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <h3 className="text-xs font-semibold text-muted-foreground uppercase tracking-wide mb-1">{title}</h3>
      {children}
    </section>
  )
}

interface SnapshotsTableProps {
  rows: SnapshotRow[]
  showCreated: boolean
  busy: string | null
  namespaceStopped: boolean
  onImport: (row: SnapshotRow) => void
  onRename: (row: SnapshotRow) => void
  onDelete: (row: SnapshotRow) => void
}

function SnapshotsTable({ rows, showCreated, busy, namespaceStopped, onImport, onRename, onDelete }: SnapshotsTableProps) {
  const { t } = useTranslation()
  return (
    <table className="w-full text-xs border-collapse">
      <thead>
        <tr className="text-left text-muted-foreground border-b border-border">
          <th className="py-1.5 pr-4 font-medium">{t('snapshots.col.name')}</th>
          <th className="py-1.5 pr-4 font-medium w-[15%]">{t('snapshots.col.size')}</th>
          {showCreated && <th className="py-1.5 pr-4 font-medium w-[25%]">{t('snapshots.col.created')}</th>}
          <th className="py-1.5 pr-0 text-right font-medium w-24">{t('journal.actions')}</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((row, i) => (
          <tr key={`${row.scope}:${row.name}:${i}`} className="border-b border-border/20 hover:bg-muted/30">
            <td className="py-[3px] pr-4 font-mono">{row.name}</td>
            <td className="py-[3px] pr-4 text-muted-foreground">{row.size}</td>
            {showCreated && <td className="py-[3px] pr-4 text-muted-foreground">{row.created}</td>}
            <td className="py-[3px] pr-0 text-right whitespace-nowrap">
              <RowButton
                icon={Download}
                title={t('snapshots.action.import')}
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
        ))}
        {rows.length === 0 && (
          <tr>
            <td colSpan={showCreated ? 4 : 3} className="py-4 text-center text-muted-foreground">
              {t('journal.noData')}
            </td>
          </tr>
        )}
      </tbody>
    </table>
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
  return (
    <button
      type="button"
      className={`inline-flex items-center justify-center p-1 rounded text-muted-foreground ${hover} hover:bg-muted disabled:opacity-40 disabled:hover:bg-transparent`}
      title={title}
      disabled={disabled}
      onClick={(e) => { e.stopPropagation(); onClick() }}
    >
      <Icon size={14} />
    </button>
  )
}

function formatBytes(bytes: number): string {
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)} GB`
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)} MB`
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`
  return `${bytes} B`
}

/** Format a Date as "HH:MM dd.MM.yyyy" — Kotlin parity (SnapshotsDialog.kt). */
function formatSnapshotDate(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(date.getHours())}:${pad(date.getMinutes())} ${pad(date.getDate())}.${pad(date.getMonth() + 1)}.${date.getFullYear()}`
}

function defaultSnapshotName(): string {
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  return `snapshot-${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}_${pad(d.getHours())}${pad(d.getMinutes())}`
}
