import { useCallback, useEffect, useState } from 'react'
import { AlertTriangle, Check, Loader2 } from 'lucide-react'
import { Modal } from './Modal'
import { LauncherUpdateHint } from './LauncherUpdateHint'
import {
  getDependencyPreflight, postDependencyMigrate,
  getDependencyRollbackPreflight, postDependencyRollback,
} from '../lib/api'
import type { DependencyDto, DependencyRollbackDto, ExistingVolume, PreflightResult } from '../lib/types'
import { useDepsStore, type DepsMigrationView } from '../lib/depsStore'
import { useTranslation, type LocaleKey } from '../lib/i18n'
import { formatBytes } from '../lib/format'
import { formatDateTime } from '../lib/datetime'
import { showError } from '../lib/errorModal'

interface Props {
  open: boolean
  onClose: () => void
}

type View =
  | { kind: 'list' }
  | { kind: 'confirm'; item: DependencyDto }
  // The rollback's confirm screen is its OWN view and not a flag on 'confirm':
  // the two share no sentence, no preflight endpoint and no button. One screen
  // with two modes would have had to be true of both, and no wording is — one
  // of them destroys the reachability of recent data and the other destroys
  // nothing.
  | { kind: 'rollbackConfirm'; item: DependencyDto; rollback: DependencyRollbackDto }
  | { kind: 'progress' }
  | { kind: 'result' }

/**
 * The steps of each plan, in order, keyed by DEPENDENCY id. Keep in sync with
 * `migrate.PostgresStepIDs()` and `migrate.CopyStepIDs()`
 * (internal/deps/migrate/preflight.go and copy_upgrade.go), which are the
 * lists the plans themselves are built from and which the Go tests and the
 * CLI's locale keys read; TypeScript cannot import them.
 *
 * PostgreSQL dumps and restores; RabbitMQ and ZooKeeper copy the data volume
 * and upgrade the copy. They share four ids out of eleven, so one hardcoded
 * list rendered a dump and a restore for a migration that does neither and hid
 * the copy that IS the operation.
 *
 * Rendering the whole list up front — rather than only the step that is
 * running — is what makes the wait legible: the user can see what is still to
 * come and that nothing has been skipped. A step id the launcher does not know
 * (a newer plan on an older UI) is appended rather than dropped, and a
 * DEPENDENCY it does not know contributes no base list at all: inventing one
 * would promise ten steps that will never happen, while the events still fill
 * the list in as they arrive.
 */
const POSTGRES_STEPS = [
  'stop-namespace', 'pull-image', 'start-source', 'dump', 'stop-source',
  'create-volume', 'start-target', 'restore', 'verify', 'stop-target',
] as const

const COPY_STEPS = [
  'stop-namespace', 'pull-image', 'create-volume', 'copy-volume',
  'start-old', 'pre-upgrade', 'stop-old', 'start-new', 'post-upgrade',
  'verify', 'stop-new',
] as const

/** Go: migrate.RollbackStepIDs(). Three steps on the migration's own progress
 *  channel — deliberately no second rendering path. */
const ROLLBACK_STEPS = ['stop-namespace', 'switch-generation', 'start-namespace'] as const

const PLAN_STEPS: Record<string, readonly string[]> = {
  postgres: POSTGRES_STEPS,
  rabbitmq: COPY_STEPS,
  zookeeper: COPY_STEPS,
  // Qdrant shares the same copy-upgrade plan as rabbitmq/zookeeper (Go:
  // migrate.registry.go's `migrators`/`rollbacks` map it to
  // QdrantMigrator{}/RollbackCopyUpgrade) — same 11 step ids, even though its
  // CopySpec sets no PreUpgrade/PostUpgrade hook: the engine still runs steps
  // named "pre-upgrade"/"post-upgrade", they just no-op. Only reachable today
  // on a daemon that has qdrant migration but no `stepIds` (this UI's
  // fallback is positional against PLAN_STEPS), but the fallback exists
  // precisely for an older/newer daemon mismatch, so a missing entry here is
  // a real gap, not a hypothetical one.
  qdrant: COPY_STEPS,
}

/** The dependencies whose plan COPIES the data volume instead of dumping it —
 *  the confirm screen describes the wrong operation otherwise. */
const copyPlan = (id: string) => PLAN_STEPS[id] === COPY_STEPS


const BTN_PRIMARY = 'rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-xs font-medium hover:bg-primary/90 disabled:opacity-50'
const BTN_SECONDARY = 'rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50'

/** The step id the daemon publishes while it has no plan yet (Go:
 *  api.DependencyMigrationStepPreparing). */
const PREPARING_STEP = 'preparing'

/**
 * The step ids to render, in the plan's real order — including repeats.
 *
 * `migration.stepIds` is the daemon's own plan.Steps, verbatim, published
 * once it exists: the daemon is the only party that knows the plan's shape,
 * and on a multi-hop copy upgrade that shape repeats ids
 * (pre-upgrade/start-new/post-upgrade once per rung, and an INTERMEDIATE
 * rung's own node-stop shares the id "stop-new" with the plan's FINAL
 * cleanup step). Preferring it is what makes a row's position decidable at
 * all for a ladder — see the caller, which positions every row against
 * `migration.stepIndex`, never against an id.
 *
 * The fallback (an older daemon that predates this field, or the brief
 * window before the first start/progress event) is the single-hop vocabulary
 * this launcher has always hardcoded, plus anything the events mention that
 * it does not cover — unaffected by any of this, since a daemon that cannot
 * send `stepIds` cannot build a ladder plan either: its ids never repeat.
 */
function stepIdsFor(migration: DepsMigrationView): string[] {
  if (migration.stepIds.length > 0) return migration.stepIds
  // A rollback runs its own three steps whatever dependency it belongs to, so
  // the kind is asked BEFORE the id: postgres' ten would be a list of things
  // that are never going to happen.
  const plan = migration.kind === ROLLBACK_KIND ? ROLLBACK_STEPS : PLAN_STEPS[migration.id]
  const ids: string[] = [...(plan ?? [])]
  for (const id of [...migration.done, migration.step]) {
    // "preparing" is the one id that is never a step. It belongs to no plan —
    // the daemon publishes it BEFORE it has one, and the store then records it
    // as finished the moment the first real step arrives — so the append below
    // put it AFTER the last step of the plan and ticked it green: a twelfth,
    // completed step of an eleven-step migration. Its own state has a spinner
    // line (isPreparing); it is never a row. Every OTHER unknown id is still
    // appended — that is how a daemon newer than this UI shows its steps.
    if (id && id !== PREPARING_STEP && !ids.includes(id)) ids.push(id)
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

/**
 * The leftover-target-volume sentence, for each of the three states of
 * `ExistingVolume.Version` (Go: migrate.ExistingVolume). They are kept apart
 * here rather than at the call site because two of them are silent about the
 * third: a real PG_VERSION names the product and the version, the "empty"
 * sentinel says there is no cluster in the volume, and "" — a dependency whose
 * data carries NO version marker (RabbitMQ, ZooKeeper) — can say neither. It
 * is not "empty": the RabbitMQ volume this was reported on held 244 KB of real
 * data, so claiming "no cluster in it" would be as wrong as the "PostgreSQL "
 * with a blank version the two-armed original printed. The volume and its size
 * are all that is known, so they are all it says.
 */
function replaceVolumeText(
  t: (key: LocaleKey, params?: Record<string, string | number>) => string,
  vol: ExistingVolume,
): string {
  const common = { volume: vol.name, size: formatBytes(vol.sizeBytes) }
  if (vol.version === '') return t('deps.preflight.replaceVolumeNoVersion', common)
  if (vol.version === 'empty') return t('deps.preflight.replaceVolumeEmpty', common)
  return t('deps.preflight.replaceVolume', { ...common, version: vol.version })
}

/** Go: deps.ResultKindRollback. "" is a migration. */
const ROLLBACK_KIND = 'rollback'

/**
 * Free space on the ONE filesystem both halves land on (Go: `smallerFree`).
 *
 * It is measured twice by two different mechanisms — a host statfs and a `df`
 * inside a container — so when they disagree the smaller is the one that can
 * run out, and it is the one the daemon judges by. A half the daemon could not
 * measure is left at 0 and reported as a problem of its own, so a 0 is skipped
 * rather than taken as the minimum: it would turn a measurement failure into a
 * claim that the disk is full.
 */
function smallerFree(pre: PreflightResult): number {
  const measured = [pre.freeHostBytes, pre.freeVolumeBytes].filter((n) => n > 0)
  return measured.length > 0 ? Math.min(...measured) : 0
}

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
  // From the store's own field rather than out of `data`: the ordinary
  // namespace fetch writes it too (NamespaceDto.dependencyRollbackPending),
  // and it is what decides whether Upgrade can do anything at all — a dialog
  // holding a payload from before an interrupted migration would otherwise
  // offer a button whose only possible answer is a 409.
  const rollbackPending = useDepsStore((s) => s.rollbackPending)
  const refresh = useDepsStore((s) => s.refresh)
  const [view, setView] = useState<View>({ kind: 'list' })
  const [preflight, setPreflight] = useState<PreflightResult | null>(null)
  const [replaceVolume, setReplaceVolume] = useState(false)
  const [starting, setStarting] = useState(false)
  // What the rollback this client started was going back TO, and for WHICH
  // dependency. The verdict's events carry no target, and the one result slot
  // still holds the migration being undone until the next refresh lands — so
  // without this the success screen would have to say "rolled back" and name
  // nothing. It is keyed by dependency because it outlives its own screen: a
  // rollback of another dependency, started from the CLI while this dialog is
  // open, would otherwise be reported as going to THIS one's version.
  const [rollbackTo, setRollbackTo] = useState<{ id: string; to: string } | null>(null)

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

  // And on the END of a migration, verdict or not. A migration that vanishes
  // with no verdict is the daemon-died case: its restart ran the recovery, and
  // if the ROLLBACK failed the journal is still open — the pin is frozen and
  // every new migration is refused with a 409. Nothing announces that: no
  // event and no result. The namespace fetch now carries the alarm itself, but
  // not the LIST: the items, the pin and the last verdict the dialog already
  // had were all read before any of it happened.
  // The store serves this from the verdict's own fetch when there was one.
  const migrating = !!migration
  useEffect(() => {
    if (open && !migrating) reload()
  }, [open, migrating, reload])

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

  const lastResult = data?.lastResult
  // Where the rollback went. This client's own click is the first source; a
  // rollback started elsewhere (the CLI, another window) is read off the
  // result slot once the post-verdict refresh has landed, and until then the
  // screen says it without naming a version rather than naming a wrong one.
  const rolledBackTo = (rollbackTo && rollbackTo.id === result?.id ? rollbackTo.to : '')
    || (lastResult?.kind === ROLLBACK_KIND && lastResult.id === result?.id ? lastResult.to : '')

  const openConfirm = (item: DependencyDto) => {
    setPreflight(null)
    setReplaceVolume(false)
    setView({ kind: 'confirm', item })
    getDependencyPreflight(item.id).then(setPreflight).catch((e) => showError(e as Error))
  }

  const openRollbackConfirm = (item: DependencyDto, rollback: DependencyRollbackDto) => {
    setPreflight(null)
    setView({ kind: 'rollbackConfirm', item, rollback })
    getDependencyRollbackPreflight(item.id).then(setPreflight).catch((e) => showError(e as Error))
  }

  /**
   * What to show once the POST has been accepted, shared by the migration and
   * the rollback because the race is the same for both: the daemon answers
   * only after it has already broadcast `deps_migration_start`, and the whole
   * operation can be over before the answer lands.
   */
  const afterAccepted = (id: string, clickedAt: number, kind: string) => {
    const s = useDepsStore.getState()
    const settled = !!s.result && s.result.id === id && s.result.at >= clickedAt
    if (settled) {
      // Nothing rendered between the start and the end, so the render-time
      // derivation below never saw a progress screen to leave — show the
      // verdict from here, where the intent (this click) is known.
      setView({ kind: 'result' })
    } else if (!s.migration) {
      s.onStart(id, 0, kind)
    }
  }

  const startRollback = async (item: DependencyDto, rollback: DependencyRollbackDto) => {
    setStarting(true)
    const clickedAt = Date.now()
    setRollbackTo({ id: item.id, to: rollback.toVersion || rollback.toImage })
    try {
      await postDependencyRollback(item.id)
      afterAccepted(item.id, clickedAt, ROLLBACK_KIND)
    } catch (e) {
      showError(e as Error)
    } finally {
      setStarting(false)
    }
  }

  const start = async (item: DependencyDto) => {
    setStarting(true)
    const clickedAt = Date.now()
    setRollbackTo(null)
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
      afterAccepted(item.id, clickedAt, '')
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
      // Neither of the next two is "update the launcher": a vendor-forbidden
      // hop is not lifted by a newer launcher, and a bundle offering something
      // OLDER is not an upgrade being held back at all. Both carry the
      // daemon's own sentence in statusDetail, which is the only part that
      // says what to do — and neither gets a <LauncherUpdateHint/>.
      case 'upgrade-blocked': return t('deps.status.blocked')
      case 'bundle-older': return t('deps.status.bundleOlder')
      case 'pending-minor': return t('deps.status.pendingMinor', { version: item.targetVersion ?? item.targetImage })
      default: return t('deps.status.upToDate')
    }
  }

  // The daemon fills StatusDetail only for the statuses a fixed label cannot
  // explain, so it is rendered wherever it is non-empty rather than switched
  // on again here (Go: api.DependencyDto.StatusDetail, "so a renderer may
  // print it unconditionally"). It is English, like every sentence built in
  // internal/deps/migrate; the LABEL above is the localized half.
  const canStart = !!preflight && preflight.ok
    && (!preflight.existingTargetVolume || replaceVolume)
    && !starting && !rollbackPending

  // A rollback has no volume to confirm and nothing to measure — only the
  // preflight's own verdict, and the two daemon states that refuse it.
  const canRollBack = !!preflight && preflight.ok && !starting && !rollbackPending && !migration

  /** Why the per-row actions are disabled, or undefined when they are not. The
   *  daemon refuses both with a 409, so a live button could only ever produce
   *  an error modal. */
  const busyReason = () => rollbackPending
    || (migration ? t(migration.kind === ROLLBACK_KIND ? 'deps.controls.rollingBack' : 'deps.controls.migrating') : undefined)

  return (
    <Modal
      open={open}
      // The rollback gets its own title: it is a different action with a
      // different risk, and "Dependencies" over a confirm screen about
      // unreachable data is not what the user is being asked about.
      title={view.kind === 'rollbackConfirm' ? t('deps.rollback.title', { id: view.item.id }) : t('deps.title')}
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
          {view.kind === 'rollbackConfirm' && (
            <>
              <button type="button" className={BTN_SECONDARY} onClick={() => setView({ kind: 'list' })}>
                {t('common.back')}
              </button>
              <button
                type="button"
                className={BTN_PRIMARY}
                disabled={!canRollBack}
                onClick={() => { void startRollback(view.item, view.rollback) }}
              >
                {starting && <Loader2 size={12} className="mr-1 inline animate-spin" />}
                {t('deps.rollback.confirm')}
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
                {/* Every cell that has another one to its right carries its
                    own trailing padding. Tailwind's preflight collapses the
                    table borders, which also drops the browser's default
                    border-spacing, so nothing else keeps two columns apart:
                    the header read "DependencyCurrent" and a long id ran
                    straight into its own version ("zookeeperzookeeper:3.9.5").
                    Padding is inside the cell box, so the separation holds at
                    every id and image length. The last column is the
                    right-aligned actions cell and has nothing after it.

                    That padding only separates two columns while the text
                    stays inside its own content box, and an auto table
                    guarantees no such thing — which is why every cell also
                    carries a wrap rule. `break-words` (here, and on the id and
                    the status prose) does NOT change intrinsic sizing, so the
                    layout is exactly what it was; it only refuses to paint
                    outside the box if the column ends up narrower than the
                    word. That distinction is load-bearing for the first
                    column in particular: an auto table hands surplus width out
                    in proportion to (max-content − min-content), and for a
                    one-word header that difference is zero, so the column is
                    exactly its own label plus the padding with nothing to
                    spare — measured at 88.4px for "Зависимость" in Chromium
                    and 88.3px in the desktop app's WebKitGTK. A rule that
                    lowered its min-content would take even that away. */}
                <tr className="border-b border-border text-left text-xs text-muted-foreground">
                  <th className="py-1 pr-4 font-medium break-words">{t('deps.col.dependency')}</th>
                  <th className="py-1 pr-4 font-medium break-words">{t('deps.col.current')}</th>
                  <th className="py-1 pr-4 font-medium break-words">{t('deps.col.available')}</th>
                  <th className="py-1 pr-4 font-medium break-words">{t('deps.col.status')}</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {data.items.map((item) => (
                  <tr key={item.id} data-testid={`dep-${item.id}`} className="border-b border-border/50">
                    {/* The dependency ID, never `item.app`: the two differ for
                        mongodb (app `mongo`), and the banner, the progress
                        panel, the verdict and `citeck deps upgrade <id>` all
                        name it by the id. One thing, one name. */}
                    <td className="py-1.5 pr-4 break-words">{item.id}</td>
                    {/* `wrap-anywhere`, not `break-words`, on these two alone:
                        it is the one of the pair that lowers the cell's
                        min-content, and that is what lets the TABLE shrink to
                        whatever the dialog gives it. Measured on the stand in
                        both engines: a registry-qualified `keycloak/keycloak:
                        26.4.5` wants 189px per image column, which put the
                        table's min-content at 647px inside a 606px dialog body
                        — the table was pushed out of the modal and the actions
                        column ended up behind a horizontal scrollbar. The
                        image is the one value here that is a single long
                        machine token with no wrap opportunity of its own, and
                        it is the one value a mid-token break costs nothing to
                        read: it is mono, so a continued line reads as one
                        string.

                        The floor is what keeps that from happening to the
                        ORDINARY images too. Lowering the min-content also
                        lowers what the column is handed when there IS room —
                        an auto table shares surplus in proportion to
                        (max-content − min-content) — and measured in the
                        desktop webview that left the column at 118.7px, three
                        pixels short of `zookeeper:3.9.5`, which then wrapped
                        with its last character alone on the second line. 15ch
                        is the longest unbreakable segment the built-in
                        dependency images produce (`zookeeper:3.9.5`, and
                        `rabbitmq:4.2.9-` of `rabbitmq:4.2.9-management`), plus
                        the 1rem gutter. It is in `ch` rather than px because
                        that is the rule itself — fifteen characters OF THE
                        FONT THIS CELL USES — so it survives a monospace
                        fallback the launcher never chose. */}
                    <td className="py-1.5 pr-4 font-mono text-xs wrap-anywhere min-w-[calc(15ch_+_1rem)]">{item.currentImage}</td>
                    <td className="py-1.5 pr-4 font-mono text-xs wrap-anywhere min-w-[calc(15ch_+_1rem)]">{item.targetImage}</td>
                    <td className="py-1.5 pr-4 text-xs break-words">
                      <div>{statusLabel(item)}</div>
                      {item.statusDetail && (
                        <div className="mt-0.5 max-w-md text-[11px] text-muted-foreground">{item.statusDetail}</div>
                      )}
                    </td>
                    <td className="space-y-1 py-1.5 text-right">
                      {item.status === 'upgrade-available' && (
                        // Disabled while a rollback is pending or a migration
                        // runs: the daemon refuses both with a 409, so the
                        // button could only ever produce an error modal.
                        <span title={busyReason()}>
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
                      {/* The offer to go back to what this dependency ran on
                          before its last migration. It is independent of the
                          status: a namespace that migrated is up to date and
                          still has somewhere to go back to. When the launcher
                          cannot use the target — the retained volume it told
                          the operator they may reclaim is gone — the reason is
                          shown INSTEAD of a button whose only answer is a
                          refusal. */}
                      {item.rollback?.available && (
                        <span title={busyReason()}>
                          <button
                            type="button"
                            className={BTN_SECONDARY}
                            disabled={!!rollbackPending || !!migration}
                            onClick={() => openRollbackConfirm(item, item.rollback!)}
                          >
                            {t('deps.rollback.action', {
                              version: item.rollback.toVersion || item.rollback.toImage,
                            })}
                          </button>
                        </span>
                      )}
                      {item.rollback && !item.rollback.available && item.rollback.problem && (
                        <div className="max-w-xs text-left text-[11px] text-muted-foreground">
                          {t('deps.rollback.unavailable', { problem: item.rollback.problem })}
                        </div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}

      {view.kind === 'confirm' && (
        <div className="space-y-2 text-sm" data-testid="deps-confirm">
          {/* The two plans do different things to the data, and one sentence
              cannot describe both: PostgreSQL dumps and restores, RabbitMQ and
              ZooKeeper copy the volume and upgrade the copy. */}
          <p>{t(copyPlan(view.item.id) ? 'deps.confirm.introCopy' : 'deps.confirm.intro',
            { id: view.item.id, from: view.item.currentImage, to: view.item.targetImage })}</p>
          {/* ZooKeeper 3.8 and 3.9 share one on-disk format, so its "migration"
              is a container swap. Saying so is the honest version of a screen
              that otherwise implies a data conversion. */}
          {view.item.id === 'zookeeper' && (
            <p className="text-xs text-muted-foreground">{t('deps.zk.note')}</p>
          )}
          {!preflight && <Loader2 size={16} className="animate-spin" />}
          {preflight && (
            <>
              <ul className="list-disc space-y-0.5 pl-5 text-xs">
                {/* A preflight the daemon refused before it touched Docker
                    measured nothing, and its zeros are not facts: rendering
                    them claims the namespace holds no data and the host has no
                    free space, above the line with the actual reason.
                    spaceChecked is the discriminator (Go: Measured()). It
                    replaced `requiredHostBytes > 0`, which stopped being true
                    the moment a plan appeared that writes NOTHING to the host:
                    a copy upgrade legitimately needs zero bytes there, and
                    reading that zero as "unmeasured" hid the one requirement
                    that does exist. Which is also why the host line is skipped
                    on its own below — printing it would claim a second
                    requirement of 0 B. */}
                {preflight.spaceChecked && (
                  <>
                    <li>{t('deps.preflight.data', { size: formatBytes(preflight.dataSizeBytes) })}</li>
                    {/* One filesystem carrying both writes is ONE line with the
                        sum. As two lines each half looks satisfiable on its own
                        while the daemon refuses the migration for wanting them
                        together — the confirm screen would be arguing with the
                        error it is about to produce. */}
                    {preflight.sharedFilesystem ? (
                      <li>{t('deps.preflight.shared', {
                        need: formatBytes(preflight.requiredTotalBytes),
                        free: formatBytes(smallerFree(preflight)),
                      })}</li>
                    ) : (
                      <>
                        {preflight.requiredHostBytes > 0 && (
                          <li>{t('deps.preflight.host', { need: formatBytes(preflight.requiredHostBytes), free: formatBytes(preflight.freeHostBytes) })}</li>
                        )}
                        <li>{t('deps.preflight.volume', { need: formatBytes(preflight.requiredVolumeBytes), free: formatBytes(preflight.freeVolumeBytes) })}</li>
                      </>
                    )}
                  </>
                )}
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
                  {/* THREE states, not two (Go: migrate.ExistingVolume.Version).
                      "empty" is the daemon's sentinel for a volume with no
                      PG_VERSION in it — the common leftover case — and it is
                      not a version, so interpolating it reads as "PostgreSQL
                      empty" in every locale. "" is the third: a dependency
                      whose data carries no version marker at all (RabbitMQ,
                      ZooKeeper), where the volume and its size are everything
                      that is known — it is NOT "empty" (the RabbitMQ volume
                      this was reported on held 244 KB of real data), and it is
                      not PostgreSQL, which is what the two-armed version of
                      this printed: "244 KB, PostgreSQL ". */}
                  <span>{replaceVolumeText(t, preflight.existingTargetVolume)}</span>
                </label>
              )}
            </>
          )}
        </div>
      )}

      {view.kind === 'rollbackConfirm' && (
        <div className="space-y-2 text-sm" data-testid="deps-rollback-confirm">
          {/* FIELDS, not prose. What this operation DOES is said once, by the
              daemon, as the preflight warnings below — the same channel that
              already carries the existing-volume warning, the deferred
              deprecated-features check and the image-not-local heads-up, and
              whose Go builder documents why those sentences are warnings
              rather than dialog body text.
              Restating them here in the user's language would have made the
              dialog correct only while the Go function emitted exactly three
              of them in exactly that position: a cross-language contract with
              a magic number in it, maintained twice (the CLI would need the
              same count).

              What IS here is what the warnings cannot carry: both volume names
              as fields the user can read off at a glance rather than out of a
              paragraph, and the DATE — which the Go preflight deliberately
              does not have, because it holds no migration result to read it
              from, so it travels on the offer instead. */}
          <dl data-testid="deps-rollback-fields" className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs">
            <dt className="text-muted-foreground">{t('deps.rollback.field.version')}</dt>
            <dd className="font-mono">{view.rollback.toVersion || view.rollback.toImage}</dd>
            <dt className="text-muted-foreground">{t('deps.rollback.field.volume')}</dt>
            <dd className="font-mono">{view.rollback.volume}</dd>
            <dt className="text-muted-foreground">{t('deps.rollback.field.frozen')}</dt>
            <dd className="font-mono">{view.rollback.frozenVolume}</dd>
            {/* Omitted rather than rendered empty when the one result slot no
                longer holds the migration this would undo: the warnings still
                say "as it was when the migration finished", which is true
                without a date, and a labelled blank is only noise. */}
            {!!view.rollback.migratedAt && (
              <>
                <dt className="text-muted-foreground">{t('deps.rollback.field.migratedAt')}</dt>
                <dd>{formatDateTime(view.rollback.migratedAt)}</dd>
              </>
            )}
          </dl>
          {!preflight && <Loader2 size={16} className="animate-spin" />}
          {preflight && (
            <>
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
            </>
          )}
        </div>
      )}

      {view.kind === 'progress' && migration && (
        <div className="space-y-2 text-sm" data-testid="deps-progress">
          <p className="text-xs text-muted-foreground">
            {t(migration.kind === ROLLBACK_KIND ? 'deps.progress.rollbackTitle' : 'deps.progress.title',
              { id: migration.id })}
          </p>
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
              //
              // A row's position is decided by StepIndex against THIS list —
              // never by matching `id` — because a ladder repeats ids: an
              // INTERMEDIATE rung's own node-stop shares "stop-new" with the
              // plan's FINAL cleanup step, so `ids.indexOf('stop-new')` would
              // resolve to whichever one is LISTED (the last), and mark every
              // row before it done — verify included — the moment the FIRST
              // rung's own stop-new ran. StepIndex is 1-based and counts the
              // step CURRENTLY running (migrate.Run: `progress(st.ID, i+1,
              // total, ...)`), so activePos below is its 0-based position.
              const activePos = migration.stepIndex > 0 ? migration.stepIndex - 1 : -1
              const seen = new Set<string>()
              return ids.map((id, pos) => {
              const done = activePos >= 0 && pos < activePos
              const active = activePos === pos
              // Ids repeat on a ladder; the FIRST occurrence keeps the plain
              // testid every existing single-hop test already asserts on, and
              // only a REPEAT gets the position appended, so nothing about a
              // non-repeating plan's ids changes.
              const testId = seen.has(id) ? `deps-step-${id}-${pos}` : `deps-step-${id}`
              seen.add(id)
              return (
                <li
                  key={`${pos}-${id}`}
                  data-testid={testId}
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
                <Check size={16} />{result.kind === ROLLBACK_KIND
                  ? rolledBackTo
                    ? t('deps.result.rolledBack.to', { id: result.id, to: rolledBackTo })
                    : t('deps.result.rolledBackDone', { id: result.id })
                  : t('deps.result.success', { id: result.id })}
              </p>
              <p className="text-xs text-muted-foreground">{result.message}</p>
              {/* The volume left behind. After a MIGRATION it is the old data,
                  which the user may reclaim; after a ROLLBACK it is the newer
                  data, which becomes unreachable — the same field, two very
                  different sentences, so the verdict's kind decides. */}
              {lastResult?.oldVolume && (
                <p className="text-xs text-muted-foreground">{t(
                  result.kind === ROLLBACK_KIND ? 'deps.result.frozenVolume' : 'deps.result.oldVolume',
                  { volume: lastResult.oldVolume },
                )}</p>
              )}
            </>
          ) : (
            <>
              <p className="flex items-center gap-1 text-destructive">
                <AlertTriangle size={16} />{t(
                  result.kind === ROLLBACK_KIND ? 'deps.result.rollbackFailed' : 'deps.result.failed',
                  { id: result.id },
                )}
              </p>
              <p className="text-xs">{result.message}</p>
              {/* A failed MIGRATION undoes itself, and says so — but only when
                  the daemon says that undo finished; a pending one is reported
                  by the list's own notice instead. A failed ROLLBACK has
                  nothing to undo: it moves the pin in one atomic write, so
                  either it happened or it did not, and every failure path
                  leaves the dependency on the version it was already running. */}
              {result.kind === ROLLBACK_KIND
                ? <p className="text-xs text-muted-foreground">{t('deps.result.rollbackUnchanged')}</p>
                : !rollbackPending && <p className="text-xs text-muted-foreground">{t('deps.result.rolledBack')}</p>}
            </>
          )}
        </div>
      )}
    </Modal>
  )
}
