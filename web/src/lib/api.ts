import type {
  NamespaceDto,
  HealthDto,
  DaemonStatusDto,
  AppInspectDto,
  ActionResultDto,
  AppFileDto,
  NamespaceSummaryDto,
  QuickStartDto,
  TemplateDto,
  NamespaceCreateDto,
  BundleInfoDto,
  SecretMetaDto,
  SecretCreateDto,
  DiagnosticsDto,
  DiagFixResultDto,
  SnapshotDto,
  RestartEventDto,
  WorkspaceDto,
  WorkspaceCreateDto,
  WorkspaceUpdateDto,
} from './types'

export const API_BASE = '/api/v1'

const CSRF_HEADER = { 'X-Citeck-CSRF': '1' }

/**
 * ApiError preserves the machine-readable `code` field that the daemon sends
 * in JSON error bodies alongside `message`. Callers can branch on `code` to
 * trigger UI flows (e.g. `ENCRYPTION_NOT_SET_UP` → run CreateMasterPwd before
 * retrying the request) without parsing error message strings.
 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  constructor(message: string, status: number, code: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

async function extractApiError(res: Response): Promise<ApiError> {
  let message = res.statusText || `HTTP ${res.status}`
  let code = ''
  try {
    const body = await res.json()
    if (typeof body.message === 'string') message = body.message
    if (typeof body.code === 'string') code = body.code
  } catch { /* not JSON, fall through with statusText */ }
  return new ApiError(message, res.status, code)
}

async function extractErrorMessage(res: Response): Promise<string> {
  return (await extractApiError(res)).message
}

function fetchWithTimeout(url: string, opts?: RequestInit, timeoutMs = 30_000): Promise<Response> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  // If caller provided a signal, forward its abort to our controller
  if (opts?.signal) {
    opts.signal.addEventListener('abort', () => controller.abort(), { once: true })
  }
  return fetch(url, { ...opts, signal: controller.signal }).finally(() => clearTimeout(timer))
}

async function fetchJSON<T>(path: string): Promise<T> {
  const res = await fetchWithTimeout(`${API_BASE}${path}`, {
    headers: { Accept: 'application/json' },
  })
  if (!res.ok) {
    throw new Error(await extractErrorMessage(res))
  }
  return res.json()
}

export async function getNamespace(): Promise<NamespaceDto> {
  return fetchJSON('/namespace')
}

export async function getHealth(): Promise<HealthDto> {
  return fetchJSON('/health')
}

export async function getDaemonStatus(): Promise<DaemonStatusDto> {
  return fetchJSON('/daemon/status')
}

export async function getAppLogs(name: string, tail = 100): Promise<string> {
  const res = await fetchWithTimeout(`${API_BASE}/apps/${name}/logs?tail=${tail}`)
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.text()
}

export async function getAppInspect(name: string): Promise<AppInspectDto> {
  return fetchJSON(`/apps/${name}/inspect`)
}

export async function postAppRestart(name: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/apps/${name}/restart`, { method: 'POST', headers: CSRF_HEADER })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

/**
 * Re-queues all PULL_FAILED apps for a fresh pull attempt. The Web UI calls
 * this after the user saves new registry credentials so the affected apps
 * pick up the secret without waiting for the auto-retry backoff window.
 */
export async function postAppsRetryPullFailed(): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/apps/retry-pull-failed`, { method: 'POST', headers: CSRF_HEADER })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function postAppStop(name: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/apps/${name}/stop`, { method: 'POST', headers: CSRF_HEADER })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function postAppStart(name: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/apps/${name}/start`, { method: 'POST', headers: CSRF_HEADER })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function postNamespaceStart(force = false): Promise<ActionResultDto> {
  const qs = force ? '?force=true' : ''
  const res = await fetchWithTimeout(`${API_BASE}/namespace/start${qs}`, { method: 'POST', headers: CSRF_HEADER })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function postNamespaceStop(): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/namespace/stop`, { method: 'POST', headers: CSRF_HEADER })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function postNamespaceReload(): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/namespace/reload`, { method: 'POST', headers: CSRF_HEADER })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

// activateNamespace switches the active namespace within the current workspace.
// Daemon rejects with 409 if the current namespace is not STOPPED.
export async function activateNamespace(id: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/namespaces/${id}/activate`, { method: 'POST', headers: CSRF_HEADER })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

// deactivateNamespace clears the workspace's namespace selection so the next
// daemon start lands on Welcome instead of re-loading the previous namespace.
// Daemon rejects with 409 if the current namespace is not STOPPED.
export async function deactivateNamespace(): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/namespaces/deactivate`, { method: 'POST', headers: CSRF_HEADER })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

/**
 * Suppress git pull operations against `host` for `durationSeconds` (default
 * 3600s = 1 hour, Kotlin parity). Wired into GitPullErrorDialog Skip: clicking
 * Skip records the failing host so subsequent pulls against siblings hosted
 * there (e.g. workspace repo + bundle repos on the same GitLab) don't
 * re-prompt within the suppression window.
 *
 * durationSeconds <= 0 clears the existing skip for that host.
 */
export async function postGitSkipPull(host: string, durationSeconds = 3600): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(
    `${API_BASE}/git/skip-pull`,
    {
      method: 'POST',
      headers: { ...CSRF_HEADER, 'Content-Type': 'application/json' },
      body: JSON.stringify({ host, durationSeconds }),
    },
  )
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

/**
 * Force-pull the default workspace repo (bypasses pull-period throttle) and
 * trigger a reload so the runtime picks up any new bundles / workspace
 * config. Kotlin parity: WelcomeScreen.kt "Force Update" RMB menu.
 *
 * Uses a longer timeout because the git pull can be slow on cold network.
 */
export async function postWorkspaceUpdate(): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(
    `${API_BASE}/workspace/update`,
    { method: 'POST', headers: CSRF_HEADER },
    180_000,
  )
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

// Multi-workspace CRUD + activate (desktop-only — server returns 404).
// listWorkspaces swallows 404 and returns [] so callers in server mode can
// transparently render "no workspaces" instead of branching on mode.
export async function listWorkspaces(): Promise<WorkspaceDto[]> {
  const res = await fetchWithTimeout(`${API_BASE}/workspaces`)
  if (res.status === 404) return []
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function createWorkspace(data: WorkspaceCreateDto): Promise<WorkspaceDto> {
  const res = await fetchWithTimeout(`${API_BASE}/workspaces`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...CSRF_HEADER },
    body: JSON.stringify(data),
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function updateWorkspace(id: string, data: WorkspaceUpdateDto): Promise<WorkspaceDto> {
  const res = await fetchWithTimeout(`${API_BASE}/workspaces/${encodeURIComponent(id)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', ...CSRF_HEADER },
    body: JSON.stringify(data),
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function deleteWorkspace(id: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/workspaces/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    headers: CSRF_HEADER,
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function activateWorkspace(id: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/workspaces/${encodeURIComponent(id)}/activate`, {
    method: 'POST',
    headers: CSRF_HEADER,
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

/**
 * Open a server-allowlisted directory in the OS file manager. The Kind is
 * resolved server-side ("volumes" → namespace volumes/runtime base).
 *
 * In server mode the daemon returns the path without opening it — the UI
 * is responsible for displaying / copying it for the user to open manually.
 */
export interface OpenDirResponse {
  opened: boolean
  path: string
  mode: 'desktop' | 'server'
  message?: string
}

export async function postOpenDir(kind: 'volumes' | 'snapshots'): Promise<OpenDirResponse> {
  const res = await fetchWithTimeout(`${API_BASE}/system/open-dir`, {
    method: 'POST',
    headers: { ...CSRF_HEADER, 'Content-Type': 'application/json' },
    body: JSON.stringify({ kind }),
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function fetchRestartEvents(): Promise<RestartEventDto[]> {
  const resp = await fetchWithTimeout(`${API_BASE}/namespace/restart-events`)
  if (!resp.ok) return []
  return resp.json()
}

export async function clearAppRestartEvents(name: string): Promise<void> {
  const res = await fetchWithTimeout(`${API_BASE}/apps/${name}/restart-events`, { method: 'DELETE', headers: CSRF_HEADER })
  if (!res.ok) throw await extractApiError(res)
}

export async function getDaemonLogs(tail = 200): Promise<string> {
  const res = await fetchWithTimeout(`${API_BASE}/daemon/logs?tail=${tail}`)
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.text()
}

export async function getSystemDump(format: 'json' | 'zip' = 'json'): Promise<void> {
  const query = format === 'zip' ? '?format=zip' : ''
  const res = await fetchWithTimeout(`${API_BASE}/system/dump${query}`, undefined, 60_000)
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = format === 'zip' ? 'system-dump.zip' : 'system-dump.json'
  a.style.display = 'none'
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  setTimeout(() => URL.revokeObjectURL(url), 5000)
}

/**
 * Desktop-only system dump. The browser <a download> path getSystemDump uses
 * is silently dropped by the WebKitGTK webview (no download handler), so the
 * desktop UI POSTs here instead: the Wails layer writes the ZIP to disk and
 * opens the containing folder (Kotlin 1.x parity), returning the saved path.
 */
export async function saveSystemDumpNative(): Promise<string> {
  const res = await fetchWithTimeout('/desktop/system-dump', { method: 'POST', headers: CSRF_HEADER }, 60_000)
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  const data = await res.json() as { path?: string }
  return data.path ?? ''
}

export async function getVolumes(): Promise<{ name: string; path: string; size?: number }[]> {
  return fetchJSON('/volumes')
}

// Lazily compute the size of a single volume on demand (the list loads without
// sizes because Docker /system/df is slow). Measures only this volume via `du`
// in a utils container. Returns size in bytes (-1 if unavailable).
export async function getVolumeSize(name: string): Promise<{ size: number }> {
  return fetchJSON(`/volumes/${encodeURIComponent(name)}/size`)
}

export async function deleteVolume(name: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/volumes/${name}`, { method: 'DELETE', headers: CSRF_HEADER })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function getAppConfig(name: string): Promise<string> {
  const res = await fetchWithTimeout(`${API_BASE}/apps/${name}/config`)
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.text()
}

export async function putAppConfig(name: string, content: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/apps/${name}/config`, {
    method: 'PUT',
    headers: { 'Content-Type': 'text/yaml', ...CSRF_HEADER },
    body: content,
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

/**
 * Resets the app's configuration to the generated default — discards any
 * user-edited ApplicationDef override. Mirrors Kotlin's AppCfgEditWindow
 * Reset button.
 */
export async function resetAppConfig(name: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/apps/${name}/config/reset`, {
    method: 'POST',
    headers: CSRF_HEADER,
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function getAppFiles(name: string): Promise<AppFileDto[]> {
  return fetchJSON<AppFileDto[]>(`/apps/${name}/files`)
}

/**
 * Discards user edits for a single mounted bind-mount file and triggers a
 * namespace reload so the original generator-supplied content is restored
 * on disk. Mirrors `resetAppConfig` but at file granularity (Kotlin parity:
 * `nsRuntime.resetEditedFile`).
 */
export async function resetAppFile(name: string, path: string): Promise<ActionResultDto> {
  const cleanPath = path.startsWith('./') ? path.slice(2) : path
  const url = `${API_BASE}/apps/${name}/files/reset?path=${encodeURIComponent(cleanPath)}`
  const res = await fetchWithTimeout(url, {
    method: 'POST',
    headers: CSRF_HEADER,
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function getAppFile(name: string, path: string): Promise<string> {
  const cleanPath = path.startsWith('./') ? path.slice(2) : path
  const res = await fetchWithTimeout(`${API_BASE}/apps/${name}/files/${cleanPath}`)
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.text()
}

export async function putAppFile(name: string, path: string, content: string): Promise<ActionResultDto> {
  const cleanPath = path.startsWith('./') ? path.slice(2) : path
  const res = await fetchWithTimeout(`${API_BASE}/apps/${name}/files/${cleanPath}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'text/plain', ...CSRF_HEADER },
    body: content,
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function putAppLock(name: string, locked: boolean): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/apps/${name}/lock`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', ...CSRF_HEADER },
    body: JSON.stringify({ locked }),
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function getConfigContent(): Promise<string> {
  const res = await fetchWithTimeout(`${API_BASE}/config`)
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.text()
}

export async function putConfigContent(content: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/config`, {
    method: 'PUT',
    headers: { 'Content-Type': 'text/yaml', ...CSRF_HEADER },
    body: content,
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

// Phase E1: Welcome Screen
export async function getNamespaces(): Promise<NamespaceSummaryDto[]> {
  return fetchJSON('/namespaces')
}

export async function deleteNamespace(id: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/namespaces/${id}`, { method: 'DELETE', headers: CSRF_HEADER })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function getTemplates(): Promise<TemplateDto[]> {
  return fetchJSON('/templates')
}

export async function getQuickStarts(): Promise<QuickStartDto[]> {
  return fetchJSON('/quick-starts')
}

// Phase E3: Namespace creation
export async function createNamespace(data: NamespaceCreateDto): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/namespaces`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...CSRF_HEADER },
    body: JSON.stringify(data),
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function getBundles(): Promise<BundleInfoDto[]> {
  return fetchJSON('/bundles')
}

/**
 * Typed namespace edit form payload. Mirrors `api.NamespaceEditDto` on the
 * daemon — see internal/api/dto.go.
 */
export interface NamespaceEditDto {
  name: string
  bundleRepo: string
  bundleKey: string
  authType: string
  users?: string[]
  host: string
  port: number
  tlsEnabled: boolean
  pgAdminEnabled: boolean
}

export async function getNamespaceEdit(): Promise<NamespaceEditDto> {
  return fetchJSON('/namespace/edit')
}

/**
 * Pre-filled defaults for the "Create namespace" dialog. Mirrors Kotlin 1.x —
 * the server reads the workspace's default template, resolves bundle defaults
 * (first repo + LATEST when the template doesn't pin one), and picks the next
 * unused "Citeck #N" name.
 */
export interface NamespaceCreateDefaultsDto {
  name: string
  bundleRepo: string
  bundleKey: string
  authType: string
  users?: string[]
}

export async function getNamespaceCreateDefaults(): Promise<NamespaceCreateDefaultsDto> {
  return fetchJSON('/namespace/create-defaults')
}

export async function putNamespaceEdit(data: NamespaceEditDto): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/namespace/edit`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', ...CSRF_HEADER },
    body: JSON.stringify(data),
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

// Phase F1: Secrets
export async function getSecrets(): Promise<SecretMetaDto[]> {
  return fetchJSON('/secrets')
}

export async function createSecret(data: SecretCreateDto): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/secrets`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...CSRF_HEADER },
    body: JSON.stringify(data),
  })
  // Use ApiError so the SecretsDialog can branch on `code === 'ENCRYPTION_NOT_SET_UP'`
  // and run CreateMasterPwd before retrying the save.
  if (!res.ok) throw await extractApiError(res)
  return res.json()
}

export interface LicenseDto {
  id: string
  tenant: string
  priority: number
  issuedTo: string
  issuedAt?: string
  validFrom?: string
  validUntil?: string
  content?: unknown
  valid: boolean
}

export async function getLicenses(): Promise<LicenseDto[]> {
  return fetchJSON('/licenses')
}

export async function createLicense(licenseJSON: string): Promise<LicenseDto> {
  // The body is raw license JSON (signed payload). We POST it through as
  // application/json so the daemon's json.Decoder consumes it directly.
  const res = await fetchWithTimeout(`${API_BASE}/licenses`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...CSRF_HEADER },
    body: licenseJSON,
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function deleteLicense(id: string): Promise<void> {
  const res = await fetchWithTimeout(`${API_BASE}/licenses/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    headers: CSRF_HEADER,
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
}

export async function deleteSecret(id: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/secrets/${id}`, { method: 'DELETE', headers: CSRF_HEADER })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function testSecret(id: string): Promise<ActionResultDto> {
  return fetchJSON(`/secrets/${id}/test`)
}

// Phase F2: Diagnostics
export async function getDiagnostics(): Promise<DiagnosticsDto> {
  return fetchJSON('/diagnostics')
}

export async function postDiagnosticsFix(): Promise<DiagFixResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/diagnostics/fix`, { method: 'POST', headers: CSRF_HEADER })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

// Phase F3: Snapshots
export async function getSnapshots(): Promise<SnapshotDto[]> {
  return fetchJSON('/snapshots')
}

export async function postExportSnapshot(name?: string): Promise<ActionResultDto> {
  const qs = name ? `?name=${encodeURIComponent(name)}` : ''
  const res = await fetchWithTimeout(`${API_BASE}/snapshots/export${qs}`, { method: 'POST', headers: CSRF_HEADER }, 300_000)
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function postImportSnapshot(file: File): Promise<ActionResultDto> {
  const formData = new FormData()
  formData.append('file', file)
  const res = await fetchWithTimeout(`${API_BASE}/snapshots/import`, {
    method: 'POST',
    headers: CSRF_HEADER,
    body: formData,
  }, 300_000)
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

/**
 * Import a snapshot already present on disk in the namespace snapshots
 * directory (Kotlin parity: `nsRuntime.importSnapshot(name)`). The daemon
 * resolves the .zip from the namespace's local snapshots/ dir and follows
 * the same restore path as the upload-based import.
 */
export async function postImportSnapshotByName(name: string): Promise<ActionResultDto> {
  const filename = name.endsWith('.zip') ? name : `${name}.zip`
  const res = await fetchWithTimeout(
    `${API_BASE}/snapshots/import?name=${encodeURIComponent(filename)}`,
    { method: 'POST', headers: CSRF_HEADER },
    300_000,
  )
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function getWorkspaceSnapshots(): Promise<{ id: string; name: string; url: string; size?: string }[]> {
  return fetchJSON('/workspace/snapshots')
}

export async function postSnapshotDownload(url: string, name?: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/snapshots/download`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...CSRF_HEADER },
    body: JSON.stringify({ url, name }),
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function renameSnapshot(oldName: string, newName: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/snapshots/${encodeURIComponent(oldName)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', ...CSRF_HEADER },
    body: JSON.stringify({ name: newName }),
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function deleteSnapshot(name: string): Promise<ActionResultDto> {
  // Daemon expects the .zip suffix; ensure it's present (caller may pass either).
  const filename = name.endsWith('.zip') ? name : `${name}.zip`
  const res = await fetchWithTimeout(`${API_BASE}/snapshots/${encodeURIComponent(filename)}`, {
    method: 'DELETE',
    headers: CSRF_HEADER,
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

// Migration
export async function getMigrationStatus(): Promise<{ hasPendingSecrets: boolean; encrypted: boolean; locked: boolean; hasSecrets: boolean }> {
  const res = await fetchWithTimeout(`${API_BASE}/migration/status`)
  if (!res.ok) return { hasPendingSecrets: false, encrypted: false, locked: false, hasSecrets: false }
  return res.json()
}

export async function submitMasterPassword(password: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/migration/master-password`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...CSRF_HEADER },
    body: JSON.stringify({ password }),
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

// Secrets encryption
export async function getSecretsStatus(): Promise<{ encrypted: boolean; locked: boolean }> {
  return fetchJSON('/secrets/status')
}

export async function unlockSecrets(password: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/secrets/unlock`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...CSRF_HEADER },
    body: JSON.stringify({ password }),
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function setupSecretsPassword(password: string): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/secrets/setup-password`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...CSRF_HEADER },
    body: JSON.stringify({ password }),
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

/**
 * Wipe all stored secrets and reset the encryption envelope. Used by the
 * AskMasterPassword "Reset Master Password and Drop All Secrets" flow.
 */
export async function resetSecrets(): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/secrets/reset`, {
    method: 'POST',
    headers: CSRF_HEADER,
  })
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
}

export async function openExternal(url: string): Promise<void> {
  // Wails desktop: webview is served through Wails AssetServer (reverse proxy),
  // so /wails/runtime is available natively. Use Browser.OpenURL API.
  try {
    const res = await fetch('/wails/runtime', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ object: 9, method: 0, args: { url } }),
    })
    if (res.ok) return
  } catch { /* not in Wails webview */ }
  // Browser fallback
  window.open(url, '_blank')
}

// -- Desktop multi-window controls --
// These endpoints are served by the Wails-only WindowManager mounted on the
// internal Wails AssetServer (see internal/desktop/windows.go). In server mode
// the path is unreachable; openDesktopWindow gracefully falls back to opening
// the route as a browser tab so the same UI code works in both deployments.

export interface DesktopWindowSpec {
  kind: 'logs' | 'editor' | 'daemon-logs'
  id?: string
  route?: string
  title?: string
  width?: number
  height?: number
}

/**
 * Opens a separate OS window for the given route. Resolves to true if the
 * Wails window manager handled it; false if we fell back to a browser tab.
 *
 * The caller does not need to know whether the launcher is running in
 * desktop mode — this helper detects /desktop/windows availability at call
 * time and routes accordingly.
 */
export async function openDesktopWindow(spec: DesktopWindowSpec): Promise<boolean> {
  try {
    const res = await fetch('/desktop/windows/open', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(spec),
    })
    if (res.ok) return true
  } catch { /* not in Wails desktop */ }
  // Browser fallback: navigate to the route in a new tab so server-mode
  // users still get a "secondary window" UX even if it is a browser tab.
  const route = spec.route ?? (spec.id ? `/window/${spec.kind}/${spec.id}` : `/window/${spec.kind}`)
  window.open(route, '_blank')
  return false
}

/**
 * Closes the currently focused secondary Wails window. Used by Cancel /
 * Esc handlers in /window/* pages where `window.close()` can be a no-op
 * inside the webview (browsers only allow it for windows opened via
 * window.open from the same script). Best-effort: silently no-ops in
 * server mode or when the window manager doesn't recognise the caller.
 */
export async function closeCurrentDesktopWindow(spec: { kind: 'logs' | 'editor' | 'daemon-logs'; id?: string }): Promise<void> {
  try {
    await fetch('/desktop/windows/close', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(spec),
    })
  } catch { /* not in Wails desktop */ }
  // Belt-and-suspenders for the server-mode browser fallback.
  window.close()
}

/** True if /desktop/windows/* is reachable (i.e. running inside Wails desktop). */
export async function hasDesktopWindowManager(): Promise<boolean> {
  try {
    const res = await fetch('/desktop/windows/list', { method: 'GET' })
    return res.ok
  } catch {
    return false
  }
}
