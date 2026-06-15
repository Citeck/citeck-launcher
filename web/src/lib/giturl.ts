/**
 * Small git-URL helpers shared by GitPullErrorDialog, SecretPicker and the
 * workspace form. Kept in lib/ (not inside a component) so both dialogs use
 * the exact same host / auth-shape heuristics.
 */

/**
 * Extracts the bare hostname (lowercased, no port) from a git URL. Supports
 * https/ssh URLs and the `git@host:path` SCP form. Mirrors the server-side
 * git.HostFromURL helper so the skip request keys on the same string the
 * daemon compares against.
 */
export function extractHost(repoUrl: string): string {
  const trimmed = (repoUrl ?? '').trim()
  if (!trimmed) return ''
  // SCP-like form: git@host:user/repo.git
  if (!trimmed.includes('://')) {
    const at = trimmed.indexOf('@')
    if (at < 0) return ''
    const rest = trimmed.slice(at + 1)
    const colon = rest.indexOf(':')
    return (colon >= 0 ? rest.slice(0, colon) : rest).toLowerCase()
  }
  try {
    return new URL(trimmed).hostname.toLowerCase()
  } catch {
    return ''
  }
}

/**
 * Heuristic: does a git pull/clone error message look like an
 * authentication/authorization failure (as opposed to e.g. a network error or
 * a missing repo)? Covers go-git's "authentication required" /
 * "authorization failed" plus common HTTP phrasings the daemon may relay.
 */
export function isAuthShapedGitError(msg: string | null | undefined): boolean {
  if (!msg) return false
  const m = msg.toLowerCase()
  return (
    m.includes('authentication required') ||
    m.includes('authentication failed') ||
    m.includes('authorization failed') ||
    m.includes('invalid credentials') ||
    m.includes('access denied') ||
    m.includes('401') ||
    m.includes('403')
  )
}
