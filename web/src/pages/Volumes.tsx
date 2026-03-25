import { useEffect, useState } from 'react'
import { getVolumes, deleteVolume } from '../lib/api'
import { ConfirmModal } from '../components/ConfirmModal'
import { Trash2 } from 'lucide-react'

interface VolumeInfo {
  name: string
  driver: string
  mountpoint: string
}

export function Volumes() {
  const [volumes, setVolumes] = useState<VolumeInfo[]>([])
  const [error, setError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)

  function loadVolumes() {
    getVolumes().then(setVolumes).catch((e) => setError(e.message))
  }

  useEffect(() => { loadVolumes() }, [])

  async function handleDelete() {
    if (!deleteTarget) return
    setDeleting(true)
    setDeleteError(null)
    try {
      await deleteVolume(deleteTarget)
      setDeleteTarget(null)
      loadVolumes()
    } catch (e) {
      setDeleteError((e as Error).message)
    } finally {
      setDeleting(false)
    }
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
