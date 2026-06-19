// Shared registry-credentials heuristics used by both the per-app drawer button
// (AppDrawerContent) and the namespace-level RegistryAuthBanner derivation in
// the dashboard store. Keep them in one place so the "is this a pull-auth
// failure for host X" decision is identical everywhere.

/** Extract the registry host from a Docker image reference, or '' when the
 *  image uses an implicit (Docker Hub) registry. Docker convention: the first
 *  path segment is a registry host only if it contains a '.' or ':'. */
export function registryHostOf(image: string | undefined): string {
  if (!image) return ''
  const slash = image.indexOf('/')
  if (slash < 0) return ''
  const head = image.slice(0, slash)
  if (!head.includes('.') && !head.includes(':')) return ''
  return head
}

/** Heuristic: does the daemon's statusText look like a pull-auth failure?
 *  Mirrors the daemon-side nsactions.IsAuthError classifier. */
export function isAuthErrorText(text: string | undefined): boolean {
  if (!text) return false
  const t = text.toLowerCase()
  return t.includes('authentication') || t.includes('unauthorized') || t.includes('401') || t.includes('denied')
}
