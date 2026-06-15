import { useState } from 'react'
import { useNavigate } from 'react-router'
import { VolumesDialog } from '../components/VolumesDialog'
import { SnapshotsDialog } from '../components/SnapshotsDialog'
import { useDashboardStore } from '../lib/store'

/**
 * /volumes route — thin wrapper around the VolumesDialog + SnapshotsDialog
 * pair (same composition the Dashboard sidebar uses) so the URL keeps
 * resolving for power users / old bookmarks.
 *
 * The dialogs are the single source of truth for volume / snapshot
 * management: per-volume delete with namespace-stopped gating, Delete All,
 * snapshot import with the destructive-overwrite confirm and the blocking
 * longOp overlay driven by SSE terminal events. The previous standalone page
 * re-implemented import/export without the confirm or the overlay.
 */
export function Volumes() {
  const navigate = useNavigate()
  const namespace = useDashboardStore((s) => s.namespace)
  const [snapshotsOpen, setSnapshotsOpen] = useState(false)
  const namespaceStopped = namespace?.status === 'STOPPED'
  return (
    <>
      <VolumesDialog
        open
        onClose={() => navigate('/')}
        onOpenSnapshots={() => setSnapshotsOpen(true)}
        namespaceStopped={namespaceStopped}
      />
      <SnapshotsDialog
        open={snapshotsOpen}
        onClose={() => setSnapshotsOpen(false)}
        namespaceStopped={namespaceStopped}
      />
    </>
  )
}
