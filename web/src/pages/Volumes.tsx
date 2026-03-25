import { useEffect, useState, useCallback, useRef } from 'react'
import { getVolumes, deleteVolume, getSnapshots, postExportSnapshot, postImportSnapshot } from '../lib/api'
import type { SnapshotDto } from '../lib/types'
import { ConfirmModal } from '../components/ConfirmModal'
import { Trash2, Download, Upload, Archive, Loader2 } from 'lucide-react'

interface VolumeInfo {
  name: string
  driver: string
  mountpoint: string
}

export function Volumes() {
  const [volumes, setVolumes] = useState<VolumeInfo[]>([])
  const [snapshots, setSnapshots] = useState<SnapshotDto[]>([])
  const [error, setError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const [exporting, setExporting] = useState(false)
  const [importing, setImporting] = useState(false)
  const [snapshotMsg, setSnapshotMsg] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const loadData = useCallback(() => {
    getVolumes().then(setVolumes).catch((e) => setError(e.message))
    getSnapshots().then(setSnapshots).catch(() => {})
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

  return (
    <div className="p-3">
      <h1 className="text-base font-semibold mb-2">Docker Volumes</h1>
      {error && <div className="text-destructive text-xs mb-2">{error}</div>}

      <table className="w-full text-xs border-collapse">
        <thead>
          <tr className="text-left text-muted-foreground border-b border-border">
            <th className="py-1 pr-4 font-medium">Name</th>
            <th className="py-1 pr-4 font-medium">Driver</th>
            <th className="py-1 font-medium text-right w-16"></th>
          </tr>
        </thead>
        <tbody>
          {volumes.map((v) => (
            <tr key={v.name} className="border-b border-border/20 hover:bg-muted/30">
              <td className="py-[3px] pr-4 font-mono">{v.name}</td>
              <td className="py-[3px] pr-4 text-muted-foreground">{v.driver}</td>
              <td className="py-[3px] text-right">
                <button type="button" className="p-1 rounded text-muted-foreground hover:text-destructive hover:bg-muted"
                  title="Delete volume" onClick={() => setDeleteTarget(v.name)}>
                  <Trash2 size={14} />
                </button>
              </td>
            </tr>
          ))}
          {volumes.length === 0 && (
            <tr><td colSpan={3} className="py-4 text-center text-muted-foreground">No volumes found</td></tr>
          )}
        </tbody>
      </table>

      {/* Snapshots section */}
      <div className="mt-6 border-t border-border pt-4">
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-sm font-semibold flex items-center gap-1.5">
            <Archive size={14} />
            Snapshots
          </h2>
          <div className="flex items-center gap-2">
            <button
              type="button"
              className="flex items-center gap-1 rounded border border-border px-2.5 py-1 text-xs hover:bg-muted disabled:opacity-50"
              onClick={handleExport}
              disabled={exporting || volumes.length === 0}
              title="Export all volumes to a snapshot ZIP"
            >
              {exporting ? <Loader2 size={13} className="animate-spin" /> : <Download size={13} />}
              {exporting ? 'Exporting...' : 'Export'}
            </button>
            <button
              type="button"
              className="flex items-center gap-1 rounded border border-border px-2.5 py-1 text-xs hover:bg-muted disabled:opacity-50"
              onClick={() => fileInputRef.current?.click()}
              disabled={importing}
              title="Import volumes from a snapshot ZIP"
            >
              {importing ? <Loader2 size={13} className="animate-spin" /> : <Upload size={13} />}
              {importing ? 'Importing...' : 'Import'}
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
                <th className="py-1 pr-4 font-medium">Name</th>
                <th className="py-1 pr-4 font-medium">Size</th>
                <th className="py-1 pr-4 font-medium">Created</th>
              </tr>
            </thead>
            <tbody>
              {snapshots.map((s) => (
                <tr key={s.name} className="border-b border-border/20 hover:bg-muted/30">
                  <td className="py-[3px] pr-4 font-mono">{s.name}</td>
                  <td className="py-[3px] pr-4 text-muted-foreground">{formatSize(s.size)}</td>
                  <td className="py-[3px] pr-4 text-muted-foreground">{new Date(s.createdAt).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <div className="text-xs text-muted-foreground py-2">No snapshots yet. Export volumes to create one.</div>
        )}
      </div>

      <ConfirmModal
        open={!!deleteTarget}
        title={`Delete volume ${deleteTarget}?`}
        message="This will permanently delete the volume and all its data."
        confirmLabel="Delete"
        confirmVariant="danger"
        loading={deleting}
        error={deleteError}
        onConfirm={handleDelete}
        onCancel={() => { setDeleteTarget(null); setDeleteError(null) }}
      />
    </div>
  )
}
