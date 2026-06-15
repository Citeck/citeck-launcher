import type {
  NamespaceDto,
  HealthDto,
  DaemonStatusDto,
  AppInspectDto,
  ActionResultDto,
  AppFileDto,
  NamespaceSummaryDto,
  QuickStartDto,
  NamespaceCreateDto,
  BundleInfoDto,
  SecretMetaDto,
  SecretCreateDto,
  SecretUpdateDto,
  DiagnosticsDto,
  DiagFixResultDto,
  SnapshotDto,
  RestartEventDto,
  WorkspaceDto,
  WorkspaceCreateDto,
  WorkspaceUpdateDto,
  UpdateStatusDto,
  ReleaseNoteDto,
} from './types'
import { notifyAuthRequired } from './authGate'

export const API_BASE = '/api/v1'

const CSRF_HEADER = { 'X-Citeck-CSRF': '1' }

/**
 * ApiError is the single error shape thrown by every API helper in this
 * module. It preserves the machine-readable `code` field that the daemon
 * sends in JSON error bodies alongside `message`, plus the HTTP `status`.
 * Callers can branch on `code` to trigger UI flows (e.g.
 * `ENCRYPTION_NOT_SET_UP` → run CreateMasterPwd before retrying the request)
 * without parsing error message strings.
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

function fetchWithTimeout(url: string, opts?: RequestInit, timeoutMs = 30_000): Promise<Response> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  // If caller provided a signal, forward its abort to our controller
  if (opts?.signal) {
    opts.signal.addEventListener('abort', () => controller.abort(), { once: true })
  }
  return fetch(url, { ...opts, signal: controller.signal }).finally(() => clearTimeout(timer))
}

/** Shorthand for path-parameter encoding so endpoint one-liners stay legible. */
const enc = encodeURIComponent

/**
 * Encodes a multi-segment path (e.g. a mounted-file path containing '/')
 * while preserving the '/' separators, so the daemon's `{path...}` wildcard
 * route still sees individual segments but special characters round-trip.
 */
function encPath(p: string): string {
  return p.split('/').map(enc).join('/')
}

type HttpMethod = 'GET' | 'POST' | 'PUT' | 'DELETE'

interface RequestOpts {
  /**
   * Request body: FormData is sent as-is (browser sets the multipart
   * boundary), strings are sent raw with `contentType` (default text/plain),
   * anything else is JSON-serialized as application/json.
   */
  body?: unknown
  /** Content-Type override for string bodies. */
  contentType?: string
  /** Per-request timeout in ms (default 30s). */
  timeout?: number
  signal?: AbortSignal
}

/**
 * Single transport for all daemon API calls: prefixes API_BASE, attaches the
 * CSRF header on mutating methods, serializes the body, and converts every
 * non-2xx response into an ApiError. Returns the raw Response so callers can
 * pick json()/text()/blob().
 */
async function rawRequest(method: HttpMethod, path: string, opts: RequestOpts = {}): Promise<Response> {
  const headers: Record<string, string> = {}
  if (method === 'GET') headers.Accept = 'application/json'
  else Object.assign(headers, CSRF_HEADER)
  let body: BodyInit | undefined
  if (opts.body instanceof FormData) {
    body = opts.body
  } else if (typeof opts.body === 'string') {
    headers['Content-Type'] = opts.contentType ?? 'text/plain'
    body = opts.body
  } else if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(opts.body)
  }
  const res = await fetchWithTimeout(`${API_BASE}${path}`, { method, headers, body, signal: opts.signal }, opts.timeout)
  if (!res.ok) {
    const err = await extractApiError(res)
    // Daemon api_auth (opt-in token auth) rejected the request — raise the
    // full-screen token prompt. The error still propagates to the caller.
    if (err.status === 401 && err.code === 'AUTH_REQUIRED') notifyAuthRequired()
    throw err
  }
  return res
}

async function request<T>(method: HttpMethod, path: string, opts?: RequestOpts): Promise<T> {
  const res = await rawRequest(method, path, opts)
  return res.json()
}

async function requestText(method: HttpMethod, path: string, opts?: RequestOpts): Promise<string> {
  const res = await rawRequest(method, path, opts)
  return res.text()
}

export async function getNamespace(): Promise<NamespaceDto> {
  return request('GET', '/namespace')
}

export async function getHealth(): Promise<HealthDto> {
  return request('GET', '/health')
}

export async function getDaemonStatus(): Promise<DaemonStatusDto> {
  return request('GET', '/daemon/status')
}

export async function getAppInspect(name: string): Promise<AppInspectDto> {
  return request('GET', `/apps/${enc(name)}/inspect`)
}

export async function postAppRestart(name: string): Promise<ActionResultDto> {
  return request('POST', `/apps/${enc(name)}/restart`)
}

/**
 * Re-queues all PULL_FAILED apps for a fresh pull attempt. The Web UI calls
 * this after the user saves new registry credentials so the affected apps
 * pick up the secret without waiting for the auto-retry backoff window.
 */
export async function postAppsRetryPullFailed(): Promise<ActionResultDto> {
  return request('POST', '/apps/retry-pull-failed')
}

export async function postAppStop(name: string): Promise<ActionResultDto> {
  return request('POST', `/apps/${enc(name)}/stop`)
}

export async function postAppStart(name: string): Promise<ActionResultDto> {
  return request('POST', `/apps/${enc(name)}/start`)
}

export async function postNamespaceStart(force = false): Promise<ActionResultDto> {
  return request('POST', `/namespace/start${force ? '?force=true' : ''}`)
}

export async function postNamespaceStop(): Promise<ActionResultDto> {
  return request('POST', '/namespace/stop')
}

export async function postNamespaceReload(): Promise<ActionResultDto> {
  return request('POST', '/namespace/reload')
}

// activateNamespace switches the active namespace within the current workspace.
// Daemon rejects with 409 if the current namespace is not STOPPED.
export async function activateNamespace(id: string): Promise<ActionResultDto> {
  return request('POST', `/namespaces/${enc(id)}/activate`)
}

// deactivateNamespace clears the workspace's namespace selection so the next
// daemon start lands on Welcome instead of re-loading the previous namespace.
// Daemon rejects with 409 if the current namespace is not STOPPED.
export async function deactivateNamespace(): Promise<ActionResultDto> {
  return request('POST', '/namespaces/deactivate')
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
  return request('POST', '/git/skip-pull', { body: { host, durationSeconds } })
}

/**
 * Force-pull the default workspace repo (bypasses pull-period throttle) and
 * trigger a reload so the runtime picks up any new bundles / workspace
 * config. Kotlin parity: WelcomeScreen.kt "Force Update" RMB menu.
 *
 * Uses a longer timeout because the git pull can be slow on cold network.
 */
export async function postWorkspaceUpdate(): Promise<ActionResultDto> {
  return request('POST', '/workspace/update', { timeout: 180_000 })
}

/**
 * Persist UI preferences (theme / locale) server-side so a desktop webview
 * localStorage wipe (e.g. after a daemon auto-update) doesn't reset them.
 * Fire-and-forget from the caller's perspective — failures are swallowed so a
 * theme toggle never surfaces an error toast.
 */
export async function putUIPrefs(prefs: { theme?: string; locale?: string }): Promise<void> {
  try {
    await rawRequest('PUT', '/ui-prefs', { body: prefs })
  } catch {
    /* best-effort: prefs also live in localStorage as the fast path */
  }
}

// Multi-workspace CRUD + activate (desktop-only — server returns 404).
// listWorkspaces swallows 404 and returns [] so callers in server mode can
// transparently render "no workspaces" instead of branching on mode.
export async function listWorkspaces(): Promise<WorkspaceDto[]> {
  try {
    return await request('GET', '/workspaces')
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return []
    throw e
  }
}

export async function createWorkspace(data: WorkspaceCreateDto): Promise<WorkspaceDto> {
  return request('POST', '/workspaces', { body: data })
}

export async function updateWorkspace(id: string, data: WorkspaceUpdateDto): Promise<WorkspaceDto> {
  return request('PUT', `/workspaces/${enc(id)}`, { body: data })
}

export async function deleteWorkspace(id: string): Promise<ActionResultDto> {
  return request('DELETE', `/workspaces/${enc(id)}`)
}

export async function activateWorkspace(id: string): Promise<ActionResultDto> {
  return request('POST', `/workspaces/${enc(id)}/activate`)
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
  return request('POST', '/system/open-dir', { body: { kind } })
}

export async function fetchRestartEvents(): Promise<RestartEventDto[]> {
  try {
    return await request('GET', '/namespace/restart-events')
  } catch (e) {
    if (e instanceof ApiError) return []
    throw e
  }
}

export async function clearAppRestartEvents(name: string): Promise<void> {
  await rawRequest('DELETE', `/apps/${enc(name)}/restart-events`)
}

export async function getSystemDump(format: 'json' | 'zip' = 'json'): Promise<void> {
  const query = format === 'zip' ? '?format=zip' : ''
  const res = await rawRequest('GET', `/system/dump${query}`, { timeout: 60_000 })
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
 *
 * Lives outside API_BASE (Wails asset-server route), hence the bespoke fetch.
 */
export async function saveSystemDumpNative(): Promise<string> {
  const res = await fetchWithTimeout('/desktop/system-dump', { method: 'POST', headers: CSRF_HEADER }, 60_000)
  if (!res.ok) throw await extractApiError(res)
  const data = await res.json() as { path?: string }
  return data.path ?? ''
}

export async function getVolumes(): Promise<{ name: string; path: string; size?: number }[]> {
  return request('GET', '/volumes')
}

// Lazily compute the size of a single volume on demand (the list loads without
// sizes because Docker /system/df is slow). Measures only this volume via `du`
// in a utils container. Returns size in bytes (-1 if unavailable).
export async function getVolumeSize(name: string): Promise<{ size: number }> {
  return request('GET', `/volumes/${enc(name)}/size`)
}

export async function deleteVolume(name: string): Promise<ActionResultDto> {
  return request('DELETE', `/volumes/${enc(name)}`)
}

export async function getAppConfig(name: string): Promise<string> {
  return requestText('GET', `/apps/${enc(name)}/config`)
}

export async function putAppConfig(name: string, content: string): Promise<ActionResultDto> {
  return request('PUT', `/apps/${enc(name)}/config`, { body: content, contentType: 'text/yaml' })
}

/**
 * Resets the app's configuration to the generated default — discards any
 * user-edited ApplicationDef override. Mirrors Kotlin's AppCfgEditWindow
 * Reset button.
 */
export async function resetAppConfig(name: string): Promise<ActionResultDto> {
  return request('POST', `/apps/${enc(name)}/config/reset`)
}

export async function getAppFiles(name: string): Promise<AppFileDto[]> {
  return request('GET', `/apps/${enc(name)}/files`)
}

/**
 * Discards user edits for a single mounted bind-mount file and triggers a
 * namespace reload so the original generator-supplied content is restored
 * on disk. Mirrors `resetAppConfig` but at file granularity (Kotlin parity:
 * `nsRuntime.resetEditedFile`).
 */
export async function resetAppFile(name: string, path: string): Promise<ActionResultDto> {
  const cleanPath = path.startsWith('./') ? path.slice(2) : path
  return request('POST', `/apps/${enc(name)}/files/reset?path=${enc(cleanPath)}`)
}

export async function getAppFile(name: string, path: string): Promise<string> {
  const cleanPath = path.startsWith('./') ? path.slice(2) : path
  return requestText('GET', `/apps/${enc(name)}/files/${encPath(cleanPath)}`)
}

export async function putAppFile(name: string, path: string, content: string): Promise<ActionResultDto> {
  const cleanPath = path.startsWith('./') ? path.slice(2) : path
  return request('PUT', `/apps/${enc(name)}/files/${encPath(cleanPath)}`, { body: content })
}

// Phase E1: Welcome Screen
export async function getNamespaces(): Promise<NamespaceSummaryDto[]> {
  return request('GET', '/namespaces')
}

export async function deleteNamespace(id: string): Promise<ActionResultDto> {
  return request('DELETE', `/namespaces/${enc(id)}`)
}

export async function getQuickStarts(): Promise<QuickStartDto[]> {
  return request('GET', '/quick-starts')
}

// Phase E3: Namespace creation
export async function createNamespace(data: NamespaceCreateDto): Promise<ActionResultDto> {
  // A snapshot triggers a synchronous server-side import (download + restore
  // into volumes) before the namespace is started, so the create response can
  // take much longer than a plain create — use a generous timeout in that case.
  return request('POST', '/namespaces', { body: data, timeout: data.snapshot ? 600_000 : 30_000 })
}

export async function getBundles(): Promise<BundleInfoDto[]> {
  return request('GET', '/bundles')
}

/**
 * Typed namespace edit form payload. Mirrors `api.NamespaceEditDto` on the
 * daemon — see internal/api/dto.go. tlsEnabled / pgAdminEnabled are optional
 * (*bool on the daemon): omitting them on PUT means "leave unchanged", only
 * an explicit true/false applies. GET always fills both.
 */
export interface NamespaceEditDto {
  name: string
  bundleRepo: string
  bundleKey: string
  authType: string
  users?: string[]
  host: string
  port: number
  tlsEnabled?: boolean
  pgAdminEnabled?: boolean
}

/**
 * Authoritative editable values for ONE namespace (scoped by id — works for
 * any listed namespace, not just the active one). The bundle repo/key come
 * back RAW: a stored "LATEST" stays "LATEST", never the display-resolved
 * concrete version, so saving doesn't silently pin a floating ref.
 */
export async function getNamespaceEdit(id: string): Promise<NamespaceEditDto> {
  return request('GET', `/namespaces/${enc(id)}/edit`)
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
  return request('GET', '/namespace/create-defaults')
}

/**
 * Saves the edit form for ONE namespace (scoped by id). Server contract:
 * empty authType / absent users = leave the stored value unchanged.
 */
export async function putNamespaceEdit(id: string, data: NamespaceEditDto): Promise<ActionResultDto> {
  return request('PUT', `/namespaces/${enc(id)}/edit`, { body: data })
}

// Phase F1: Secrets
export async function getSecrets(): Promise<SecretMetaDto[]> {
  return request('GET', '/secrets')
}

// Throws ApiError so the SecretsDialog can branch on
// `code === 'ENCRYPTION_NOT_SET_UP'` and run CreateMasterPwd before retrying.
export async function createSecret(data: SecretCreateDto): Promise<ActionResultDto> {
  return request('POST', '/secrets', { body: data })
}

/**
 * Write-only partial edit (PUT /secrets/{id}): empty/absent fields keep their
 * stored values — an empty `value` keeps the old secret value, so the UI can
 * edit name/scope/username without ever fetching the value. Returns the
 * updated meta (never the value); 404 → ApiError code SECRET_NOT_FOUND.
 */
export async function updateSecret(id: string, data: SecretUpdateDto): Promise<SecretMetaDto> {
  return request('PUT', `/secrets/${enc(id)}`, { body: data })
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
  return request('GET', '/licenses')
}

/**
 * Effective enterprise-license summary (GET /licenses/status).
 * `tenant` empty/absent = no license records at all (community install);
 * `enterprise: false` with a tenant = records exist but none validates
 * (expired) — the dashboard indicator renders that state in red/grey.
 */
export interface LicenseStatusDto {
  enterprise: boolean
  tenant?: string
  issuedTo?: string
  validUntil?: string
  daysLeft: number
  expiringSoon: boolean
}

// 404s on daemons that predate the endpoint — callers treat any failure as
// "no license info" and hide the indicator.
export async function getLicenseStatus(): Promise<LicenseStatusDto> {
  return request('GET', '/licenses/status')
}

export async function createLicense(licenseJSON: string): Promise<LicenseDto> {
  // The body is raw license JSON (signed payload). We POST it through as
  // application/json so the daemon's json.Decoder consumes it directly.
  return request('POST', '/licenses', { body: licenseJSON, contentType: 'application/json' })
}

export async function deleteLicense(id: string): Promise<void> {
  await rawRequest('DELETE', `/licenses/${enc(id)}`)
}

export async function deleteSecret(id: string): Promise<ActionResultDto> {
  return request('DELETE', `/secrets/${enc(id)}`)
}

// Registry auth bindings (host → secret id) for the active workspace. Lets one
// stored REGISTRY_AUTH credential be reused per host instead of re-entered.
export async function getRegistryBindings(): Promise<Record<string, string>> {
  return request('GET', '/registry-bindings')
}

// Bind a registry host to a stored secret (empty secretId removes the binding).
// The daemon rebuilds the auth cache and retries pull-failed apps on success.
export async function setRegistryBinding(host: string, secretId: string): Promise<ActionResultDto> {
  return request('POST', '/registry-bindings', { body: { host, secretId } })
}

// Auth-required registry hosts with no resolvable credential yet. The Web UI
// checks this before starting a namespace and blocks the start until resolved.
export async function getMissingRegistryAuth(): Promise<string[]> {
  return request('GET', '/registry-bindings/missing')
}

export async function testSecret(id: string): Promise<ActionResultDto> {
  return request('GET', `/secrets/${enc(id)}/test`)
}

// Phase F2: Diagnostics
export async function getDiagnostics(): Promise<DiagnosticsDto> {
  return request('GET', '/diagnostics')
}

export async function postDiagnosticsFix(): Promise<DiagFixResultDto> {
  return request('POST', '/diagnostics/fix')
}

// Phase F3: Snapshots
export async function getSnapshots(): Promise<SnapshotDto[]> {
  return request('GET', '/snapshots')
}

export async function postExportSnapshot(name?: string): Promise<ActionResultDto> {
  const qs = name ? `?name=${enc(name)}` : ''
  return request('POST', `/snapshots/export${qs}`, { timeout: 300_000 })
}

export async function postImportSnapshot(file: File): Promise<ActionResultDto> {
  const formData = new FormData()
  formData.append('file', file)
  return request('POST', '/snapshots/import', { body: formData, timeout: 300_000 })
}

/**
 * Import a snapshot already present on disk in the namespace snapshots
 * directory (Kotlin parity: `nsRuntime.importSnapshot(name)`). The daemon
 * resolves the .zip from the namespace's local snapshots/ dir and follows
 * the same restore path as the upload-based import.
 */
export async function postImportSnapshotByName(name: string): Promise<ActionResultDto> {
  const filename = name.endsWith('.zip') ? name : `${name}.zip`
  return request('POST', `/snapshots/import?name=${enc(filename)}`, { timeout: 300_000 })
}

export async function getWorkspaceSnapshots(): Promise<{ id: string; name: string; url: string; size?: string }[]> {
  return request('GET', '/workspace/snapshots')
}

export async function postSnapshotDownload(url: string, name?: string): Promise<ActionResultDto> {
  return request('POST', '/snapshots/download', { body: { url, name } })
}

export async function renameSnapshot(oldName: string, newName: string): Promise<ActionResultDto> {
  return request('PUT', `/snapshots/${enc(oldName)}`, { body: { name: newName } })
}

export async function deleteSnapshot(name: string): Promise<ActionResultDto> {
  // Daemon expects the .zip suffix; ensure it's present (caller may pass either).
  const filename = name.endsWith('.zip') ? name : `${name}.zip`
  return request('DELETE', `/snapshots/${enc(filename)}`)
}

// Migration
export async function getMigrationStatus(): Promise<{ hasPendingSecrets: boolean; encrypted: boolean; locked: boolean; hasSecrets: boolean }> {
  try {
    return await request('GET', '/migration/status')
  } catch (e) {
    if (e instanceof ApiError) {
      return { hasPendingSecrets: false, encrypted: false, locked: false, hasSecrets: false }
    }
    throw e
  }
}

export async function submitMasterPassword(password: string): Promise<ActionResultDto> {
  return request('POST', '/migration/master-password', { body: { password } })
}

// Secrets encryption
export async function unlockSecrets(password: string): Promise<ActionResultDto> {
  return request('POST', '/secrets/unlock', { body: { password } })
}

export async function setupSecretsPassword(password: string): Promise<ActionResultDto> {
  return request('POST', '/secrets/setup-password', { body: { password } })
}

/**
 * Wipe all stored secrets and reset the encryption envelope. Used by the
 * AskMasterPassword "Reset Master Password and Drop All Secrets" flow.
 */
export async function resetSecrets(): Promise<ActionResultDto> {
  return request('POST', '/secrets/reset')
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

/**
 * Closes every secondary Wails window (logs / editor). Called when navigating
 * back to the Welcome screen so windows tied to the previous namespace don't
 * linger. Kotlin parity: WorkspaceServices.setSelectedNamespace →
 * CiteckWindow.closeAll(). Best-effort: silently no-ops in server mode (the
 * endpoint is unreachable and browser tabs can't be closed programmatically).
 */
export async function closeAllDesktopWindows(): Promise<void> {
  try {
    await fetch('/desktop/windows/close-all', { method: 'POST' })
  } catch { /* not in Wails desktop */ }
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

// --- Desktop auto-update (desktop-only — 404 in server mode) ---

export async function getUpdateStatus(): Promise<UpdateStatusDto> {
  return request('GET', '/desktop/update/status')
}

export async function checkUpdate(): Promise<UpdateStatusDto> {
  return request('POST', '/desktop/update/check')
}

export async function getUpdateChangelog(locale: string): Promise<ReleaseNoteDto[]> {
  return request('GET', `/desktop/update/changelog?locale=${enc(locale)}`)
}

export async function applyUpdate(): Promise<{ applying: boolean; version: string }> {
  return request('POST', '/desktop/update/apply', { timeout: 120_000 })
}
