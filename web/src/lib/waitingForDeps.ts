import type { AppDto } from './types'
import type { LocaleKey } from './i18n'

type Translate = (key: LocaleKey, params?: Record<string, string | number>) => string
type TranslateDynamic = (key: string, params?: Record<string, string | number>) => string

/**
 * What the walk actually reads off an app: its name (the map key, and the seed
 * of the visiting set) and the dependencies it is waiting on. Deliberately not
 * `Pick<AppDto, …>` with everything required — a caller has a full AppDto and
 * satisfies this structurally, while a test fixture should not have to invent a
 * status and a kind to ask one question about a dependency list.
 */
type WalkNode = { name?: string; waitingFor?: AppDto['waitingFor'] }

/** The app the sentence is about: a WalkNode plus the daemon's held verdict. */
type HeldApp = WalkNode & { held?: boolean }

/**
 * The detached apps behind a HELD app's hold — the ones the operator can
 * actually start. Deduped, in the order the walk meets them.
 *
 * It is a WALK, not a filter of `app.waitingFor`. A held app's unmet
 * dependencies are, by construction, either a detached root or ANOTHER app held
 * by the same rule — and on the default topology it is the second: `citeck stop
 * zookeeper` leaves gateway held on zookeeper and proxy held on gateway, so
 * proxy's own list names only gateway, an app the operator never stopped and
 * cannot start (a restart is a no-op on DEPS_WAITING). A one-level filter finds
 * no root there at all. The CLI walks for exactly this reason
 * (`output.HeldRootsForApp`), and the two must not answer differently.
 *
 * The visiting set is what keeps a hand-edited state file describing a cycle
 * from hanging the UI thread; the runtime's own walk carries the same guard.
 */
function heldRootsOf(app: HeldApp, apps: WalkNode[]) {
  const byName = new Map(apps.map((a) => [a.name, a]))
  const seen = new Set<string>()
  const roots: NonNullable<AppDto['waitingFor']> = []
  // An app with no name cannot be revisited by name — and cannot appear in
  // `apps` either, so it is enqueued exactly once and the walk still ends.
  const visiting = new Set<string>(app.name ? [app.name] : [])
  const queue: WalkNode[] = [app]
  while (queue.length > 0) {
    const cur = queue.shift()!
    for (const dep of cur.waitingFor ?? []) {
      const next = byName.get(dep.app)
      // Not DEPS_WAITING is exactly the detached root. A dependency the DTO
      // does not carry is one too: it is not an app parked in DEPS_WAITING, so
      // there is nothing to walk through.
      if (!next || dep.status !== 'DEPS_WAITING') {
        if (!seen.has(dep.app)) {
          seen.add(dep.app)
          roots.push(dep)
        }
        continue
      }
      if (!next.name || visiting.has(next.name)) continue
      visiting.add(next.name)
      queue.push(next)
    }
  }
  return roots
}

/**
 * "Waiting for: qdrant (Stopped)" — why the daemon is holding this app, worded
 * HERE rather than received ready-made.
 *
 * The daemon sends `waitingFor` as {app, status} pairs because it cannot know
 * the language: it builds that list on the runtime loop, which has no reader,
 * and one daemon serves a UI in one locale and a CLI in another at the same
 * moment. Both halves of the sentence already exist in THIS asset — the
 * `status.*` labels are the ones every StatusBadge renders — so wording it
 * here costs one key and keeps the dependency's status translated instead of
 * showing the raw `STOPPED` the daemon uses internally.
 *
 * The status gate lives on the daemon (`appWaitingForDeps`), which sends the
 * field only while the app is DEPS_WAITING; there is deliberately no second
 * copy of that rule here, so there is exactly one place it can be wrong.
 *
 * For a HELD app the list is narrowed to the DETACHED ROOTS, through however
 * many held apps the chain runs — see heldRootsOf. `apps` must be the WHOLE
 * namespace app list: a chain crosses Kind groups routinely (proxy is core,
 * alfresco is additional, alfresco-postgres is third-party), so a list narrowed
 * to one group cannot see the intermediate app and would report IT as the root
 * — the very defect the walk exists to remove. With no list, or when the walk
 * comes out empty (a cycle does), the app's own list is shown unchanged,
 * because the daemon owns the held verdict and a shape we did not expect must
 * still say something rather than fall silent.
 *
 * Returns null when there is nothing to say, so a caller can fall back to
 * `statusText` with `??`.
 */
export function waitingForDepsText(
  app: HeldApp,
  t: Translate,
  tDynamic: TranslateDynamic,
  // REQUIRED, with no default. Both production callers always have the list,
  // and the empty case is the DEGRADED one: with nothing to walk, a held app
  // falls back to its own waitingFor, which is the pre-walk behaviour — naming
  // an intermediate app the operator cannot start. A default would make that
  // the path of least resistance and, worse, invisible to the typechecker; an
  // explicit `[]` is what makes choosing it deliberate.
  apps: WalkNode[],
): string | null {
  let deps = app.waitingFor
  if (!deps || deps.length === 0) return null
  if (app.held) {
    const roots = heldRootsOf(app, apps)
    if (roots.length > 0) deps = roots
  }
  // Runtime-assembled key from the daemon's status string — the same sanctioned
  // tDynamic escape hatch StatusBadge uses, with the same fallback (an unknown
  // status renders as its key rather than disappearing).
  const list = deps.map((d) => `${d.app} (${tDynamic('status.' + d.status)})`).join(', ')
  return t('app.status.waitingForDeps', { deps: list })
}
