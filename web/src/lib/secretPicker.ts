import { getSecrets, createSecret } from './api'
import type { SecretMetaDto, SecretCreateDto, WorkspaceDto } from './types'
import type { LocaleKey } from './i18n'

/**
 * Pure logic behind components/SecretPicker.tsx and the auth-error flow in
 * GitPullErrorDialog — kept in lib/ so the component files export only
 * components (react-refresh constraint) and the save/decision mappings stay
 * unit-testable without a DOM.
 */

/** Slug for secret ids: lowercase, [a-z0-9-], no leading/trailing dashes. */
export function slugFromName(name: string): string {
  const slug = name
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return slug || 'token'
}

/**
 * Generates a collision-tolerant secret id from a display name:
 * `git-token-<slug>`, suffixed `-2`, `-3`, … while the id is taken.
 */
export function generateSecretId(name: string, existingIds: Iterable<string>): string {
  const taken = new Set(existingIds)
  const base = `git-token-${slugFromName(name)}`
  if (!taken.has(base)) return base
  for (let i = 2; ; i++) {
    const candidate = `${base}-${i}`
    if (!taken.has(candidate)) return candidate
  }
}

/**
 * Pure mapping from the "Add new…" modal fields to a GIT_TOKEN create
 * payload with a generated collision-free id. Returns null when the name or
 * token is missing (callers surface a user-facing message first).
 */
export function buildGitTokenCreate(
  name: string,
  token: string,
  existingIds: Iterable<string>,
): SecretCreateDto | null {
  const trimmed = name.trim()
  if (!trimmed || !token) return null
  const id = generateSecretId(trimmed, existingIds)
  return { id, name: trimmed, type: 'GIT_TOKEN', value: token }
}

/**
 * Creates a GIT_TOKEN secret from the add-new modal fields and returns the
 * generated id. The list is fetched fresh at save time so the id avoids
 * collisions with secrets created since the picker mounted; a failed fetch
 * degrades to an empty collision set — the daemon still rejects a true
 * duplicate.
 */
export async function createGitTokenSecret(name: string, token: string): Promise<string> {
  const existing = await getSecrets().catch(() => [] as SecretMetaDto[])
  const payload = buildGitTokenCreate(name, token, existing.map((s) => s.id))
  if (!payload) throw new Error('secret name and token are required')
  await createSecret(payload)
  return payload.id
}

/**
 * Resolves which secret a workspace's repo auth actually uses: the
 * explicitly linked `secretId`, or the legacy per-workspace "ws:<id>:repo"
 * secret when authType=TOKEN with no link (Kotlin-migrated workspaces), or
 * '' when the workspace doesn't authenticate at all.
 */
export function workspaceSecretInUse(
  ws: Pick<WorkspaceDto, 'id' | 'authType' | 'secretId'> | undefined,
): string {
  if (!ws) return ''
  if (ws.secretId) return ws.secretId
  if (ws.authType === 'TOKEN') return `ws:${ws.id}:repo`
  return ''
}

/**
 * Save-and-retry decision for GitPullErrorDialog: the workspace link only
 * needs a PUT when the user picked a DIFFERENT secret. Re-editing the VALUE
 * of the currently linked secret (same id) retries as-is — the daemon
 * resolves the fresh value by id.
 */
export function needsWorkspaceRelink(currentSecretId: string, selectedSecretId: string): boolean {
  return !!selectedSecretId && selectedSecretId !== currentSecretId
}

/** Names of the workspaces whose repo auth references this secret id. */
export function workspacesUsingSecret(
  secretId: string,
  workspaces: Pick<WorkspaceDto, 'id' | 'name' | 'secretId'>[],
): string[] {
  if (!secretId) return []
  return workspaces.filter((w) => w.secretId === secretId).map((w) => w.name || w.id)
}

/**
 * Delete-confirm text shared by SecretsDialog and the picker's row delete;
 * appends a warning when any workspace references the secret via secretId
 * (those workspaces would lose repo access).
 */
export function secretDeleteMessage(
  t: (key: LocaleKey, params?: Record<string, string | number>) => string,
  name: string,
  usedBy: string[],
): string {
  const base = t('secrets.delete.message', { name })
  if (usedBy.length === 0) return base
  return `${base} ${t('secrets.delete.usedByWorkspaces', { names: usedBy.join(', ') })}`
}
