import { useEffect, useMemo, useState } from 'react'
import { Modal, ModalField } from './Modal'
import { LoadingLabel } from './LoadingLabel'
import { getVolumes, getVolumeSize } from '../lib/api'
import { formatBytes } from '../lib/format'
import { useTranslation } from '../lib/i18n'

interface VolumeRow {
  name: string
  size?: number
}

interface SnapshotCreateDialogProps {
  open: boolean
  /** Existing snapshot names (sans .zip) for the duplicate-name check. */
  existingNames: string[]
  /** Default name pre-filled when the dialog opens. */
  defaultName: string
  loading: boolean
  onCancel: () => void
  /** Create with the chosen name and the checked volumes (their list `name`s). */
  onCreate: (name: string, volumes: string[]) => void
}

type SizeState = { status: 'idle' | 'loading' | 'done'; bytes?: number }

/**
 * Create-snapshot dialog: a name field plus a checklist of the namespace's
 * volumes so the operator can drop volumes they don't need from the snapshot.
 * All volumes start checked (the historical "snapshot everything" default) and
 * the selection is not remembered between opens. Volume sizes are computed
 * lazily per row (the daemon walks each volume with `du`, which is slow) —
 * mirroring the Volumes dialog's "Compute" affordance.
 */
export function SnapshotCreateDialog({
  open, existingNames, defaultName, loading, onCancel, onCreate,
}: SnapshotCreateDialogProps) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [volumes, setVolumes] = useState<VolumeRow[]>([])
  const [volumesLoading, setVolumesLoading] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [sizes, setSizes] = useState<Record<string, SizeState>>({})

  // Reset transient state the moment the dialog opens — the "adjust state during
  // render" pattern (same as FormDialog), so the synchronous resets don't live
  // in an effect. The async volume load runs in the effect below.
  const [prevOpen, setPrevOpen] = useState(open)
  if (open !== prevOpen) {
    setPrevOpen(open)
    if (open) {
      setName(defaultName)
      setSizes({})
      setVolumes([])
      setSelected(new Set())
      setVolumesLoading(true)
    }
  }

  // Load the namespace's volumes when the dialog opens; all start selected.
  useEffect(() => {
    if (!open) return
    let cancelled = false
    getVolumes()
      .then((vols) => {
        if (cancelled) return
        setVolumes(vols)
        setSelected(new Set(vols.map((v) => v.name)))
      })
      .catch(() => { if (!cancelled) { setVolumes([]); setSelected(new Set()) } })
      .finally(() => { if (!cancelled) setVolumesLoading(false) })
    return () => { cancelled = true }
  }, [open])

  const cleanName = name.trim().replace(/\.zip$/, '')
  const nameError = useMemo(() => {
    if (!cleanName) return ''
    if (!/^[\w\-.]+$/.test(cleanName)) return t('snapshots.field.name.invalid')
    if (existingNames.some((n) => n.replace(/\.zip$/, '') === cleanName)) return t('snapshots.field.name.alreadyExists')
    return ''
  }, [cleanName, existingNames, t])

  const canSubmit = !!cleanName && !nameError && selected.size > 0 && !loading

  function toggle(volName: string) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(volName)) next.delete(volName)
      else next.add(volName)
      return next
    })
  }

  function toggleAll() {
    setSelected((prev) => (prev.size === volumes.length ? new Set() : new Set(volumes.map((v) => v.name))))
  }

  function computeSize(volName: string) {
    setSizes((s) => ({ ...s, [volName]: { status: 'loading' } }))
    getVolumeSize(volName)
      .then(({ size }) => setSizes((s) => ({ ...s, [volName]: { status: 'done', bytes: size >= 0 ? size : undefined } })))
      .catch(() => setSizes((s) => ({ ...s, [volName]: { status: 'done' } })))
  }

  function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!canSubmit) return
    onCreate(cleanName, [...selected])
  }

  const allChecked = volumes.length > 0 && selected.size === volumes.length

  return (
    <Modal
      open={open}
      title={t('snapshots.create.title')}
      onClose={onCancel}
      onSubmit={submit}
      footer={
        <div className="flex w-full items-center justify-end gap-2">
          <button type="button" className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted" onClick={onCancel} disabled={loading}>
            {t('common.cancel')}
          </button>
          <button type="submit" className="rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-xs font-medium hover:bg-primary/90 disabled:opacity-50" disabled={!canSubmit}>
            <LoadingLabel loading={loading}>{t('snapshots.create')}</LoadingLabel>
          </button>
        </div>
      }
    >
      <ModalField label={t('snapshots.field.name')} required error={nameError}>
        <input
          autoFocus
          className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="my-snapshot"
        />
      </ModalField>

      <div>
        <div className="mb-1 flex items-center justify-between">
          <label className="flex items-center gap-2 text-xs font-medium">
            <input
              type="checkbox"
              className="rounded border-border align-middle m-0"
              checked={allChecked}
              onChange={toggleAll}
              disabled={volumesLoading || volumes.length === 0}
            />
            {t('snapshots.create.volumes')}
          </label>
          <span className="text-[11px] text-muted-foreground">{selected.size}/{volumes.length}</span>
        </div>
        <div className="max-h-56 overflow-y-auto rounded border border-border divide-y divide-border/40">
          {volumesLoading ? (
            <div className="px-2.5 py-2 text-xs text-muted-foreground">…</div>
          ) : volumes.length === 0 ? (
            <div className="px-2.5 py-2 text-xs text-muted-foreground">{t('snapshots.create.noVolumes')}</div>
          ) : (
            volumes.map((v) => {
              const st = sizes[v.name]
              return (
                <div key={v.name} className="flex items-center gap-2 px-2.5 py-1.5 text-xs hover:bg-muted/30">
                  {/* Toggle only via the checkbox + name, not the whole row —
                      clicking the size column / empty space must not flip it. */}
                  <label className="flex min-w-0 flex-1 items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      className="rounded border-border align-middle m-0 shrink-0"
                      checked={selected.has(v.name)}
                      onChange={() => toggle(v.name)}
                    />
                    <span className="truncate font-mono">{v.name}</span>
                  </label>
                  {st?.status === 'done' && st.bytes != null ? (
                    <span className="text-muted-foreground tabular-nums">{formatBytes(st.bytes)}</span>
                  ) : st?.status === 'done' ? (
                    <span className="text-muted-foreground">—</span>
                  ) : (
                    <button
                      type="button"
                      className="text-primary hover:underline disabled:opacity-50"
                      onClick={() => computeSize(v.name)}
                      disabled={st?.status === 'loading'}
                    >
                      {st?.status === 'loading' ? '…' : t('volumes.table.size')}
                    </button>
                  )}
                </div>
              )
            })
          )}
        </div>
      </div>
    </Modal>
  )
}
