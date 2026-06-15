/**
 * Pure stage derivation for the Welcome quick-start progress stepper.
 *
 * Maps a snapshot of REAL daemon state (namespace status + per-app statuses
 * from NamespaceDto, live pull progress from `pull_progress` SSE events) onto
 * five coarse bootstrap stages:
 *
 *   bundle → images → infra → apps → ready
 *
 * No polling and no invented fields — the inputs are exactly what the
 * dashboard store already maintains from the existing SSE stream:
 *   - app statuses (app_status events → debounced getNamespace refetch):
 *     READY_TO_PULL / PULLING / READY_TO_START / DEPS_WAITING / STARTING /
 *     RUNNING / STOPPED / *_FAILED / STALLED (see StatusBadge.tsx)
 *   - namespace status (namespace_status events → same refetch):
 *     STOPPED / STARTING / RUNNING / STOPPING / STALLED (api.NsStatus*)
 *   - pullProgress map (pull_progress events, percent + phase per app)
 *
 * Stage semantics:
 *   bundle — the createNamespace request (repo clone + bundle resolution +
 *            container generation) is still in flight, or the namespace has
 *            not shown up in the store yet.
 *   images — at least one app is still queued for / performing an image pull
 *            (READY_TO_PULL | PULLING).
 *   infra  — third-party apps (postgres, mongo, rabbitmq, zookeeper,
 *            keycloak, proxy, …; kind === THIRD_PARTY) not all RUNNING.
 *   apps   — Citeck webapps (kind CITECK_*) not all RUNNING.
 *   ready  — namespace status is RUNNING.
 *
 * Completion is cascading (a stage can only be done when every earlier stage
 * is done), so the done flags form a monotone prefix and the active stage is
 * simply the first incomplete one. Detached apps (status STOPPED during a
 * start) are excluded from all counts — the runtime never pulls or starts
 * them.
 */

export type StageId = 'bundle' | 'images' | 'infra' | 'apps' | 'ready'
export type StageState = 'pending' | 'active' | 'done' | 'error'

/** Subset of AppDto the derivation needs. */
export interface StageApp {
  name: string
  status: string
  kind: string
}

/** Live pull progress for one app (store.PullProgress shape). */
export interface StagePull {
  percent: number
  phase: string
}

export interface StartProgressInput {
  /** True while the createNamespace HTTP request is still in flight. */
  creating: boolean
  /** NamespaceDto.status, or null when no namespace is loaded yet. */
  nsStatus: string | null
  /** NamespaceDto.apps (may be empty before the first fetch lands). */
  apps: StageApp[]
  /** Dashboard-store pull progress map (app name → live progress). */
  pullProgress: Record<string, StagePull>
}

/** Live pull line for the images stage detail ("postgres: 45% …"). */
export interface PullLine {
  name: string
  percent: number
  phase: string
}

export interface StageInfo {
  id: StageId
  state: StageState
  /** Completed units for the stage (images pulled / apps running). */
  done?: number
  /** Total units for the stage. */
  total?: number
  /** Live pull lines, highest percent first (images stage only). */
  pulls?: PullLine[]
  /** Most relevant in-flight app for infra/apps stages. */
  currentApp?: { name: string; status: string }
  /** The app whose failure put the stage into the error state. */
  failedApp?: { name: string; status: string }
}

export interface StartProgressModel {
  stages: StageInfo[]
  /** Namespace reached RUNNING — every stage is done. */
  running: boolean
  /** Some stage is in the error state. */
  failed: boolean
}

export const STAGE_ORDER: readonly StageId[] = ['bundle', 'images', 'infra', 'apps', 'ready']

const PULL_STATES = new Set(['READY_TO_PULL', 'PULLING'])
const UP_STATES = new Set(['RUNNING', 'HEALTHY'])
const FAILED_STATES = new Set(['FAILED', 'PULL_FAILED', 'START_FAILED', 'STOPPING_FAILED', 'STALLED'])

/** Picks the app whose status is "furthest along" as the current detail line. */
const CURRENT_PRIORITY = ['STARTING', 'DEPS_WAITING', 'READY_TO_START', 'UPDATING']

function isUp(a: StageApp): boolean {
  return UP_STATES.has(a.status)
}

function pickCurrent(group: StageApp[]): { name: string; status: string } | undefined {
  for (const status of CURRENT_PRIORITY) {
    const app = group.find((a) => a.status === status)
    if (app) return { name: app.name, status: app.status }
  }
  const notUp = group.find((a) => !isUp(a))
  return notUp ? { name: notUp.name, status: notUp.status } : undefined
}

export function deriveStartProgress(input: StartProgressInput): StartProgressModel {
  const nsRunning = input.nsStatus === 'RUNNING'
  // STOPPED apps during a start are detached — the runtime skips them.
  const active = input.apps.filter((a) => a.status !== 'STOPPED')
  const infra = active.filter((a) => a.kind === 'THIRD_PARTY')
  const citeck = active.filter((a) => a.kind !== 'THIRD_PARTY')

  if (nsRunning) {
    return {
      stages: STAGE_ORDER.map((id) => ({ id, state: 'done' as StageState })),
      running: true,
      failed: false,
    }
  }

  const pulling = active.filter((a) => PULL_STATES.has(a.status))
  const bundleDone = !input.creating && active.length > 0
  const imagesDone = bundleDone && pulling.length === 0
  const infraDone = imagesDone && infra.every(isUp)
  const appsDone = infraDone && citeck.every(isUp)
  const doneFlags = [bundleDone, imagesDone, infraDone, appsDone, false]
  let activeIdx = doneFlags.indexOf(false)
  if (activeIdx < 0) activeIdx = STAGE_ORDER.length - 1

  // Failure attribution: PULL_FAILED → images; START_FAILED/FAILED on a
  // third-party app → infra; on a Citeck app → apps. A STALLED namespace
  // without a specific failed app marks whatever stage is currently active.
  const failedApp = active.find((a) => FAILED_STATES.has(a.status))
  let errorIdx = -1
  if (failedApp) {
    if (failedApp.status === 'PULL_FAILED') errorIdx = 1
    else errorIdx = failedApp.kind === 'THIRD_PARTY' ? 2 : 3
  } else if (input.nsStatus === 'STALLED') {
    errorIdx = activeIdx
  }

  const stages: StageInfo[] = STAGE_ORDER.map((id, i) => {
    const state: StageState =
      i === errorIdx ? 'error' : doneFlags[i] ? 'done' : i === activeIdx ? 'active' : 'pending'
    const stage: StageInfo = { id, state }

    if (id === 'images' && (state === 'active' || state === 'error')) {
      stage.done = active.length - pulling.length
      stage.total = active.length
      stage.pulls = pulling
        .filter((a) => a.name in input.pullProgress)
        .map((a) => ({
          name: a.name,
          percent: input.pullProgress[a.name].percent,
          phase: input.pullProgress[a.name].phase,
        }))
        .sort((a, b) => b.percent - a.percent)
    }
    if ((id === 'infra' || id === 'apps') && (state === 'active' || state === 'error')) {
      const group = id === 'infra' ? infra : citeck
      stage.done = group.filter(isUp).length
      stage.total = group.length
      stage.currentApp = pickCurrent(group)
    }
    if (state === 'error' && failedApp) {
      stage.failedApp = { name: failedApp.name, status: failedApp.status }
    }
    return stage
  })

  return { stages, running: false, failed: errorIdx >= 0 }
}
