import { useEffect, useState, useCallback, useRef } from 'react'
import { getVolumes, deleteVolume, getSnapshots, postExportSnapshot, postImportSnapshot, getWorkspaceSnapshots, postSnapshotDownload, renameSnapshot } from '../lib/api'
import type { SnapshotDto } from '../lib/types'
import { ConfirmModal } from '../components/ConfirmModal'
import { toast } from '../lib/toast'
import { useTranslation } from '../lib/i18n'
import { Trash2, Download, Upload, Archive, Loader2, Pencil, Cloud } from 'lucide-react'

interface WsSnapshot {
  id: string
  name: string
  url: string
  size?: string
}


interface VolumeInfo {
  name: string
  path: string
}

export function Volumes() {
  const { t } = useTranslation()
  const [volumes, setVolumes] = useState<VolumeInfo[]>([])
  const [snapshots, setSnapshots] = useState<SnapshotDto[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const [exporting, setExporting] = useState(false)
  const [importing, setImporting] = useState(false)
  const [snapshotMsg, setSnapshotMsg] = useState<string | null>(null)
  const [wsSnapshots, setWsSnapshots] = useState<WsSnapshot[]>([])
  const [downloading, setDownloading] = useState<string | null>(null)
  const [renaming, setRenaming] = useState<string | null>(null)
  const [renameValue, setRenameValue] = useState('')
  const fileInputRef = useRef<HTMLInputElement>(null)

  const loadData = useCallback(() => {
    setLoading(true)
    Promise.all([
      getVolumes().then(setVolumes).catch((e) => setError(e.message)),
      getSnapshots().then(setSnapshots).catch(() => {}),
      getWorkspaceSnapshots().then(setWsSnapshots).catch(() => {}),
    ]).finally(() => setLoading(false))
  }, [])

  useEffect(() => { loadData() }, [loadData])

  async function handleDelete() {
    if (!deleteTarget) return
    setDeleting(true)
    setDeleteError(null)
    try {
      await deleteVolume(deleteTarget)
      setDeleteTarget(null)
      loadData()
      toast(t('volumes.delete.success'), 'success')
    } catch (e) {
      setDeleteError((e as Error).message)
    } finally {
      setDeleting(false)
    }
  }

  async function handleExport() {
    setExporting(true)
    setSnapshotMsg(null)
    try {
      const result = await postExportSnapshot()
      setSnapshotMsg(result.message)
      loadData()
    } catch (e) {
      setSnapshotMsg(`Export failed: ${(e as Error).message}`)
    } finally {
      setExporting(false)
    }
  }

  async function handleImportFile(file: File) {
    setImporting(true)
    setSnapshotMsg(null)
    try {
      const result = await postImportSnapshot(file)
      setSnapshotMsg(result.message)
      loadData()
    } catch (e) {
      setSnapshotMsg(`Import failed: ${(e as Error).message}`)
    } finally {
      setImporting(false)
    }
  }

  function formatSize(bytes: number): string {
    if (bytes >= 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`
    if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
    if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`
    return `${bytes} B`
  }

  if (loading) {
    return (
      <div className="p-3 space-y-3">
        <div className="h-5 w-32 bg-muted rounded animate-pulse" />
        {Array.from({ length: 5 }).map((_, i) => (
          <div key={i} className="h-6 w-full bg-muted rounded animate-pulse" />
        ))}
      </div>
    )
  }

  return (
    <div className="p-3">
      <h1 className="text-base font-semibold mb-2">{t('volumes.title')}</h1>
      {error && <div className="text-destructive text-xs mb-2">{error}</div>}

      <table className="w-full text-xs border-collapse">
        <thead>
          <tr className="text-left text-muted-foreground border-b border-border">
            <th className="py-1 pr-4 font-medium">{t('volumes.table.name')}</th>
            <th className="py-1 pr-4 font-medium">{t('volumes.table.path')}</th>
            <th className="py-1 font-medium text-right w-16"></th>
          </tr>
        </thead>
        <tbody>
          {volumes.map((v) => (
            <tr key={v.name} className="border-b border-border/20 hover:bg-muted/30">
              <td className="py-[3px] pr-4 font-mono">{v.name}</td>
              <td className="py-[3px] pr-4 text-muted-foreground text-[11px] font-mono truncate max-w-xs" title={v.path}>{v.path}</td>
              <td className="py-[3px] text-right">
                <button type="button" className="p-1 rounded text-muted-foreground hover:text-destructive hover:bg-muted"
                  title={t('volumes.delete.tooltip')} onClick={() => setDeleteTarget(v.name)}>
                  <Trash2 size={14} />
                </button>
              </td>
            </tr>
          ))}
          {volumes.length === 0 && (
            <tr><td colSpan={3} className="py-4 text-center text-muted-foreground">{t('volumes.empty')}</td></tr>
          )}
        </tbody>
      </table>

      {/* Snapshots section */}
      <div className="mt-6 border-t border-border pt-4">
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-sm font-semibold flex items-center gap-1.5">
            <Archive size={14} />
            {t('volumes.snapshots')}
          </h2>
          <div className="flex items-center gap-2">
            <button
              type="button"
              className="flex items-center gap-1 rounded border border-border px-2.5 py-1 text-xs hover:bg-muted disabled:opacity-50"
              onClick={handleExport}
              disabled={exporting || volumes.length === 0}
              title={t('volumes.snapshots.export.tooltip')}
            >
              {exporting ? <Loader2 size={13} className="animate-spin" /> : <Download size={13} />}
              {exporting ? t('volumes.snapshots.exporting') : t('volumes.snapshots.export')}
            </button>
            <button
              type="button"
              className="flex items-center gap-1 rounded border border-border px-2.5 py-1 text-xs hover:bg-muted disabled:opacity-50"
              onClick={() => fileInputRef.current?.click()}
              disabled={importing}
              title={t('volumes.snapshots.import.tooltip')}
            >
              {importing ? <Loader2 size={13} className="animate-spin" /> : <Upload size={13} />}
              {importing ? t('volumes.snapshots.importing') : t('volumes.snapshots.import')}
            </button>
            <input
              ref={fileInputRef}
              type="file"
              accept=".zip"
              className="hidden"
              onChange={(e) => {
                const file = e.target.files?.[0]
                if (file) handleImportFile(file)
                e.target.value = ''
              }}
            />
          </div>
        </div>

        {snapshotMsg && (
          <div className="text-xs mb-2 rounded border border-border bg-muted/30 px-2 py-1.5">{snapshotMsg}</div>
        )}

        {snapshots.length > 0 ? (
          <table className="w-full text-xs border-collapse">
            <thead>
              <tr className="text-left text-muted-foreground border-b border-border">
                <th className="py-1 pr-4 font-medium">{t('volumes.snapshots.name')}</th>
                <th className="py-1 pr-4 font-medium">{t('volumes.snapshots.size')}</th>
                <th className="py-1 pr-4 font-medium">{t('volumes.snapshots.created')}</th>
              </tr>
            </thead>
            <tbody>
              {snapshots.map((s) => (
                <tr key={s.name} className="border-b border-border/20 hover:bg-muted/30">
                  <td className="py-[3px] pr-4 font-mono">
                    {renaming === s.name ? (
                      <form className="inline-flex gap-1" onSubmit={async (e) => {
                        e.preventDefault()
                        try { await renameSnapshot(s.name, renameValue); setRenaming(null); loadData() } catch (e) { setSnapshotMsg(`Rename failed: ${(e as Error).message}`) }
                      }}>
                        <input className="border border-border rounded px-1 bg-background text-foreground text-xs font-mono w-40"
                          value={renameValue} onChange={(e) => setRenameValue(e.target.value)} autoFocus />
                        <button type="submit" className="text-primary text-[10px]">{t('volumes.snapshots.rename.ok')}</button>
                        <button type="button" className="text-muted-foreground text-[10px]" onClick={() => setRenaming(null)}>{t('volumes.snapshots.rename.cancel')}</button>
                      </form>
                    ) : (
                      <span className="inline-flex items-center gap-1">
                        {s.name}
                        <button type="button" className="p-0.5 text-muted-foreground hover:text-foreground"
                          title={t('volumes.snapshots.rename')} onClick={() => { setRenaming(s.name); setRenameValue(s.name) }}>
                          <Pencil size={10} />
                        </button>
                      </span>
                    )}
                  </td>
                  <td className="py-[3px] pr-4 text-muted-foreground">{formatSize(s.size)}</td>
                  <td className="py-[3px] pr-4 text-muted-foreground">{new Date(s.createdAt).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <div className="text-xs text-muted-foreground py-2">{t('volumes.snapshots.empty')}</div>
        )}
      </div>

      {/* Workspace snapshots section */}
      {wsSnapshots.length > 0 && (
        <div className="mt-6 border-t border-border pt-4">
          <h2 className="text-sm font-semibold flex items-center gap-1.5 mb-3">
            <Cloud size={14} />
            {t('volumes.workspace')}
          </h2>
          <table className="w-full text-xs border-collapse">
            <thead>
              <tr className="text-left text-muted-foreground border-b border-border">
                <th className="py-1 pr-4 font-medium">{t('volumes.workspace.name')}</th>
                <th className="py-1 pr-4 font-medium">{t('volumes.workspace.size')}</th>
                <th className="py-1 font-medium text-right w-24"></th>
              </tr>
            </thead>
            <tbody>
              {wsSnapshots.map((ws) => (
                <tr key={ws.id} className="border-b border-border/20 hover:bg-muted/30">
                  <td className="py-[3px] pr-4 font-mono">{ws.name}</td>
                  <td className="py-[3px] pr-4 text-muted-foreground">{ws.size || '—'}</td>
                  <td className="py-[3px] text-right">
                    <button type="button"
                      className="flex items-center gap-1 rounded border border-border px-2 py-0.5 text-xs hover:bg-muted disabled:opacity-50 ml-auto"
                      disabled={downloading === ws.id}
                      onClick={async () => {
                        setDownloading(ws.id); setSnapshotMsg(null)
                        try {
                          const result = await postSnapshotDownload(ws.url, ws.id)
                          setSnapshotMsg(result.message); loadData()
                        } catch (e) { setSnapshotMsg(`Download failed: ${(e as Error).message}`) }
                        finally { setDownloading(null) }
                      }}>
                      {downloading === ws.id ? <Loader2 size={11} className="animate-spin" /> : <Download size={11} />}
                      {t('volumes.workspace.download')}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <ConfirmModal
        open={!!deleteTarget}
        title={t('volumes.delete.title', { name: deleteTarget || '' })}
        message={t('volumes.delete.message')}
        confirmLabel={t('common.delete')}
        confirmVariant="danger"
        loading={deleting}
        error={deleteError}
        onConfirm={handleDelete}
        onCancel={() => { setDeleteTarget(null); setDeleteError(null) }}
      />
    </div>
  )
}
