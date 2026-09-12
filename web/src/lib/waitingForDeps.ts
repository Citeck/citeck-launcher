import type { AppDto } from './types'
import type { LocaleKey } from './i18n'

type Translate = (key: LocaleKey, params?: Record<string, string | number>) => string
type TranslateDynamic = (key: string, params?: Record<string, string | number>) => string

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
 * Returns null when there is nothing to say, so a caller can fall back to
 * `statusText` with `??`.
 */
export function waitingForDepsText(
  app: Pick<AppDto, 'waitingFor'>,
  t: Translate,
  tDynamic: TranslateDynamic,
): string | null {
  const deps = app.waitingFor
  if (!deps || deps.length === 0) return null
  // Runtime-assembled key from the daemon's status string — the same sanctioned
  // tDynamic escape hatch StatusBadge uses, with the same fallback (an unknown
  // status renders as its key rather than disappearing).
  const list = deps.map((d) => `${d.app} (${tDynamic('status.' + d.status)})`).join(', ')
  return t('app.status.waitingForDeps', { deps: list })
}
