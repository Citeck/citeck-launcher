/**
 * Human-readable byte size (B / KB / MB / GB). Returns '—' for missing,
 * zero or negative input. Single source of truth — previously duplicated with
 * diverging GB precision across VolumesDialog / SnapshotsDialog / ImageDetailsModal.
 */
export function formatBytes(bytes?: number): string {
  if (!bytes || bytes <= 0) return '—'
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)} GB`
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)} MB`
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`
  return `${bytes} B`
}
