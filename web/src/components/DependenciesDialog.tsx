import { useCallback, useEffect, useState } from 'react'
import { AlertTriangle, Check, Loader2 } from 'lucide-react'
import { Modal } from './Modal'
import { LauncherUpdateHint } from './LauncherUpdateHint'
import { getDependencyPreflight, postDependencyMigrate } from '../lib/api'
import type { DependencyDto, PreflightResult } from '../lib/types'
import { useDepsStore, type DepsMigrationView } from '../lib/depsStore'
import { useTranslation } from '../lib/i18n'
import { formatBytes } from '../lib/format'
import { showError } from '../lib/errorModal'

interface Props {
  open: boolean
  onClose: () => void
}

type View =
  | { kind: 'list' }
  | { kind: 'confirm'; item: DependencyDto }
  | { kind: 'progress' }
  | { kind: 'result' }

/**
 * The steps of the PostgreSQL plan, in order. Keep in sync with
 * `migrate.PostgresStepIDs()` (internal/deps/migrate/preflight.go), which is
 * the list the plan itself is built from and which the Go tests and the CLI's
 * locale keys read; TypeScript cannot import it.
 *
 * Rendering the whole list up front — rather than only the step that is
 * running — is what makes the wait legible: the user can see what is still to
 * come and that nothing has been skipped. A step id the launcher does not know
 * (a newer plan on an older UI) is appended rather than dropped.
 */
const STEP_IDS = [
  'stop-namespace', 'pull-image', 'start-source', 'dump', 'stop-source',
  'create-volume', 'start-target', 'restore', 'verify', 'stop-target',
] as const

const BTN_PRIMARY = 'rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-xs font-medium hover:bg-primary/90 disabled:opacity-50'
const BTN_SECONDARY = 'rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50'

/** The step ids to render: the known plan, plus anything the events mention that it does not cover. */
function stepIdsFor(migration: DepsMigrationView): string[] {
  const ids: string[] = [...STEP_IDS]
  for (const id of [...migration.done, migration.step]) {
    if (id && !ids.includes(id)) ids.push(id)
  }
  return ids
}

/**
 * A step count of 0 means the daemon has no plan yet — the "preparing" state
 * it publishes while it runs the preflight, which on a real cluster is minutes
 * of `du`. The step list is meaningless then (nothing in it has been decided,
 * let alone started), so the dialog shows one spinner line instead.
 */
function isPreparing(migration: DepsMigrationView): boolean {
  return migration.stepCount === 0
}

/** The step id the daemon publishes while it has no plan yet (Go:
 *  api.DependencyMigrationStepPreparing). */
const PREPARING_STEP = 'preparing' 

/**
 * What each infrastructure dependency runs on, what the bundle offers, and the
 * migration flow (confirm → progress → result) for the ones the launcher can
 * migrate. Closing never cancels a migration; reopening shows the current step
 * (the store is fed by SSE and re-hydrated from the namespace DTO).
 */
export function DependenciesDialog({ open, onClose }: Props) {
  const { t, tDynamic } = useTranslation()
  /**
   * A step id this launcher has no key for — a newer daemon's plan — renders as
   * the raw ID, never as the bare "deps.step.<id>" lookup key, which is what
   * tDynamic answers for a miss. Same rule as the CLI's stepTitle.
   */
  const stepLabel = (id: string) => {
    const key = `deps.step.${id}`
    const label = tDynamic(key)
    return label === key ? id : label
  }
  const migration = useDepsStore((s) => s.migration)
  const result = useDepsStore((s) => s.result)
  const clearResult = useDepsStore((s) => s.clearResult)
  const data = useDepsStore((s) => s.data)
  const refresh = useDepsStore((s) => s.refresh)
  const [view, setView] = useState<View>({ kind: 'list' })
  const [preflight, setPreflight] = useState<PreflightResult | null>(null)
  const [replaceVolume, setReplaceVolume] = useState(false)
  const [starting, setStarting] = useState(false)

  const reload = useCallback(() => {
    refresh().catch((e) => showError(e as Error))
  }, [refresh])

  // Reload on open, and again once a migration has produced a verdict — that
  // is what moves the version pin, the last result and the pending-rollback
  // state the list reports.
  const resultAt = result?.at ?? 0
  useEffect(() => {
    if (open) reload()
  }, [open, reload, resultAt])

  // Follow the migration wherever the dialog is: a running one shows progress,
  // and its end shows the verdict. Derived during render (React's documented
  // way to adjust state from changed inputs) rather than in an effect, which
  // would cascade a second render and trips react-hooks/set-state-in-effect.
  if (migration && view.kind !== 'progress') setView({ kind: 'progress' })
  else if (!migration && view.kind === 'progress') {
    // A migration can vanish WITHOUT a verdict: the daemon died mid-migration
    // and its restart's recovery rolled back silently, or the namespace went
    // away under us — hydrate(null) then clears it. Without this arm the
    // dialog sat on a progress screen with nothing to render: a blank modal
    // body and no way to tell what happened.
    setView(result ? { kind: 'result' } : { kind: 'list' })
  }

  const rollbackPending = data?.rollbackPending ?? ''
  const lastResult = data?.lastResult

  const openConfirm = (item: DependencyDto) => {
    setPreflight(null)
    setReplaceVolume(false)
    setView({ kind: 'confirm', item })
    getDependencyPreflight(item.id).then(setPreflight).catch((e) => showError(e as Error))
  }

  const start = async (item: DependencyDto) => {
    setStarting(true)
    const clickedAt = Date.now()
    try {
      await postDependencyMigrate(item.id, replaceVolume)
      // The daemon answers 202 once the plan is built and has already
      // broadcast `deps_migration_start` — but a client that missed the frame
      // would otherwise sit on the confirm screen with nothing happening.
      //
      // Only when there is nothing better to show. The whole migration can be
      // over before the 202 lands (the goroutine is launched before the
      // response is written), and an unconditional start would then null a
      // verdict that had already arrived: an empty progress screen forever,
      // with the error lost. A migration already in the store needs no help
      // either — `onStart` would be a no-op with worse information.
      const s = useDepsStore.getState()
      const settled = !!s.result && s.result.id === item.id && s.result.at >= clickedAt
      if (settled) {
        // Nothing rendered between the start and the end, so the render-time
        // derivation below never saw a progress screen to leave — show the
        // verdict from here, where the intent (this click) is known.
        setView({ kind: 'result' })
      } else if (!s.migration) {
        s.onStart(item.id, 0)
      }
    } catch (e) {
      showError(e as Error)
    } finally {
      setStarting(false)
    }
  }

  const close = () => {
    if (view.kind === 'result') clearResult()
    setView({ kind: 'list' })
    onClose()
  }

  const statusLabel = (item: DependencyDto) => {
    switch (item.status) {
      case 'upgrade-available': return t('deps.status.upgradeAvailable')
      case 'requires-launcher-update': return t('deps.status.requiresLauncherUpdate')
      case 'pending-minor': return t('deps.status.pendingMinor', { version: item.targetVersion ?? item.targetImage })
      default: return t('deps.status.upToDate')
    }
  }

  const canStart = !!preflight && preflight.ok
    && (!preflight.existingTargetVolume || replaceVolume)
    && !starting && !rollbackPending

  return (
    <Modal
      open={open}
      title={t('deps.title')}
      onClose={close}
      width="lg"
      footer={
        <div className="flex w-full justify-end gap-2">
          {view.kind === 'confirm' && (
            <>
              <button type="button" className={BTN_SECONDARY} onClick={() => setView({ kind: 'list' })}>
                {t('common.back')}
              </button>
              <button
                type="button"
                className={BTN_PRIMARY}
                disabled={!canStart}
                onClick={() => { void start(view.item) }}
              >
                {starting && <Loader2 size={12} className="mr-1 inline animate-spin" />}
                {t('deps.confirm.start')}
              </button>
            </>
          )}
          {/* Close is NEVER disabled (UpdateDialog rule): closing cancels
              nothing — the migration runs on the daemon and the dialog picks it
              up again from the store when it is reopened. */}
          <button type="button" className={BTN_SECONDARY} onClick={close}>
            {t('common.close')}
          </button>
        </div>
      }
    >
      {view.kind === 'list' && (
        <div className="space-y-3">
          {rollbackPending && (
            <div
              role="alert"
              className="flex items-start gap-2 rounded border border-red-500/40 bg-red-500/10 px-2.5 py-2 text-xs text-red-600 dark:text-red-400"
            >
              <AlertTriangle size={14} className="mt-0.5 shrink-0" />
              <span>{t('deps.rollbackPending', { message: rollbackPending })}</span>
            </div>
          )}
          {lastResult && (
            <p data-testid="deps-last-result" className={`text-xs ${lastResult.success ? 'text-muted-foreground' : 'text-destructive'}`}>
              {lastResult.success
                ? t('deps.lastResult.success', { id: lastResult.id, from: lastResult.from, to: lastResult.to })
                : t('deps.lastResult.failed', {
                  id: lastResult.id, from: lastResult.from, to: lastResult.to, error: lastResult.error ?? '',
                })}
            </p>
          )}
          {data && data.items.length === 0 && (
            <p className="text-xs text-muted-foreground">{t('deps.empty')}</p>
          )}
          {data && data.items.length > 0 && (
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border text-left text-xs text-muted-foreground">
                  <th className="py-1 font-medium">{t('deps.col.dependency')}</th>
                  <th className="py-1 font-medium">{t('deps.col.current')}</th>
                  <th className="py-1 font-medium">{t('deps.col.available')}</th>
                  <th className="py-1 font-medium">{t('deps.col.status')}</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {data.items.map((item) => (
                  <tr key={item.id} data-testid={`dep-${item.id}`} className="border-b border-border/50">
                    <td className="py-1.5">{item.app}</td>
                    <td className="py-1.5 font-mono text-xs">{item.currentImage}</td>
                    <td className="py-1.5 font-mono text-xs">{item.targetImage}</td>
                    <td className="py-1.5 text-xs">{statusLabel(item)}</td>
                    <td className="py-1.5 text-right">
                      {item.status === 'upgrade-available' && (
                        // Disabled while a rollback is pending or a migration
                        // runs: the daemon refuses both with a 409, so the
                        // button could only ever produce an error modal.
                        <span title={rollbackPending || (migration ? t('deps.controls.migrating') : undefined)}>
                          <button
                            type="button"
                            className={BTN_PRIMARY}
                            disabled={!!rollbackPending || !!migration}
                            onClick={() => openConfirm(item)}
                          >
                            {t('deps.upgrade')}
                          </button>
                        </span>
                      )}
                      {item.status === 'requires-launcher-update' && <LauncherUpdateHint />}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}

      {view.kind === 'confirm' && (
        <div className="space-y-2 text-sm">
          <p>{t('deps.confirm.intro', { id: view.item.app, from: view.item.currentImage, to: view.item.targetImage })}</p>
          {!preflight && <Loader2 size={16} className="animate-spin" />}
          {preflight && (
            <>
              <ul className="list-disc space-y-0.5 pl-5 text-xs">
                <li>{t('deps.preflight.data', { size: formatBytes(preflight.dataSizeBytes) })}</li>
                <li>{t('deps.preflight.host', { need: formatBytes(preflight.requiredHostBytes), free: formatBytes(preflight.freeHostBytes) })}</li>
                <li>{t('deps.preflight.volume', { need: formatBytes(preflight.requiredVolumeBytes), free: formatBytes(preflight.freeVolumeBytes) })}</li>
                {preflight.wasRunning && <li>{t('deps.preflight.willStop')}</li>}
                <li>{t('deps.preflight.oldKept')}</li>
              </ul>
              {(preflight.warnings ?? []).map((w) => (
                <p key={w} className="flex items-start gap-1 text-xs text-amber-600 dark:text-amber-400">
                  <AlertTriangle size={14} className="mt-0.5 shrink-0" />{w}
                </p>
              ))}
              {(preflight.problems ?? []).map((p) => (
                <p key={p} role="alert" className="flex items-start gap-1 text-xs text-destructive">
                  <AlertTriangle size={14} className="mt-0.5 shrink-0" />{p}
                </p>
              ))}
              {preflight.existingTargetVolume && (
                <label className="flex items-start gap-2 text-xs text-amber-600 dark:text-amber-400">
                  <input
                    type="checkbox"
                    checked={replaceVolume}
                    onChange={(e) => setReplaceVolume(e.target.checked)}
                  />
                  {/* "empty" is the daemon's sentinel for a volume with no
                      PG_VERSION in it — the common leftover case — and it is
                      not a version, so interpolating it reads as "PostgreSQL
                      empty" in every locale. */}
                  <span>{preflight.existingTargetVolume.version === 'empty'
                    ? t('deps.preflight.replaceVolumeEmpty', {
                      volume: preflight.existingTargetVolume.name,
                      size: formatBytes(preflight.existingTargetVolume.sizeBytes),
                    })
                    : t('deps.preflight.replaceVolume', {
                      volume: preflight.existingTargetVolume.name,
                      size: formatBytes(preflight.existingTargetVolume.sizeBytes),
                      version: preflight.existingTargetVolume.version,
                    })}</span>
                </label>
              )}
            </>
          )}
        </div>
      )}

      {view.kind === 'progress' && migration && (
        <div className="space-y-2 text-sm" data-testid="deps-progress">
          <p className="text-xs text-muted-foreground">{t('deps.progress.title', { id: migration.id })}</p>
          {isPreparing(migration) && (
            <p data-testid="deps-preparing" className="flex items-center gap-2 text-xs">
              <Loader2 size={14} className="shrink-0 animate-spin" />
              <span>{stepLabel(migration.step || PREPARING_STEP)}</span>
            </p>
          )}
          {!isPreparing(migration) && (
          <ol className="space-y-1">
            {(() => {
              const ids = stepIdsFor(migration)
              // The engine runs the plan strictly in order, so everything
              // BEFORE the running step is finished whether or not this client
              // saw its progress event — which is exactly the case after a
              // reconnect, where the whole list would otherwise render as
              // untouched around a step in the middle.
              const activePos = ids.indexOf(migration.step)
              return ids.map((id, pos) => {
              const done = migration.done.includes(id) || (activePos >= 0 && pos < activePos)
              const active = migration.step === id
              return (
                <li
                  key={id}
                  data-testid={`deps-step-${id}`}
                  data-state={done ? 'done' : active ? 'active' : 'todo'}
                  className={`flex items-center gap-2 text-xs ${active ? 'font-medium' : done ? 'opacity-70' : 'opacity-40'}`}
                >
                  {done
                    ? <Check size={14} className="shrink-0 text-success" />
                    : active
                      ? <Loader2 size={14} className="shrink-0 animate-spin" />
                      : <span className="inline-block w-3.5 shrink-0" />}
                  <span>{stepLabel(id)}</span>
                  {active && migration.percent > 0 && (
                    <span className="ml-auto tabular-nums">{Math.round(migration.percent)}%</span>
                  )}
                </li>
              )
              })
            })()}
          </ol>
          )}
          {migration.percent > 0 && (
            <div className="h-1 w-full overflow-hidden rounded bg-muted">
              <div className="h-full bg-primary transition-all" style={{ width: `${Math.min(100, Math.round(migration.percent))}%` }} />
            </div>
          )}
          {migration.messages.length > 0 && (
            <div className="space-y-0.5">
              {migration.messages.map((m, i) => (
                <p key={`${i}-${m}`} className="truncate text-[11px] text-muted-foreground" title={m}>{m}</p>
              ))}
            </div>
          )}
        </div>
      )}

      {view.kind === 'result' && result && (
        <div className="space-y-2 text-sm" data-testid="deps-result" role="status">
          {result.success ? (
            <>
              <p className="flex items-center gap-1 text-success">
                <Check size={16} />{t('deps.result.success', { id: result.id })}
              </p>
              <p className="text-xs text-muted-foreground">{result.message}</p>
              {lastResult?.oldVolume && (
                <p className="text-xs text-muted-foreground">{t('deps.result.oldVolume', { volume: lastResult.oldVolume })}</p>
              )}
            </>
          ) : (
            <>
              <p className="flex items-center gap-1 text-destructive">
                <AlertTriangle size={16} />{t('deps.result.failed', { id: result.id })}
              </p>
              <p className="text-xs">{result.message}</p>
              {/* Only when the daemon says the rollback finished — a pending
                  one is reported by the list's own notice instead. */}
              {!rollbackPending && <p className="text-xs text-muted-foreground">{t('deps.result.rolledBack')}</p>}
            </>
          )}
        </div>
      )}
    </Modal>
  )
}
