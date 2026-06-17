export interface ActionResultDto {
  success: boolean
  message: string
}

export interface AppDto {
  name: string
  status: string
  statusText?: string
  image: string
  cpu: string
  memory: string
  /** 0..100; 0 when no memory limit is configured. */
  memoryPercent?: number
  /** True when memory usage >= 80% of the configured limit. */
  memoryWarning?: boolean
  /** True when memory usage >= 95% of the configured limit. */
  memoryCritical?: boolean
  /** True when the container hit its CPU quota in the latest stats sample. */
  cpuThrottled?: boolean
  kind: string
  ports?: string[]
  edited?: boolean
  locked?: boolean
  restartCount?: number
  editedFilesCount?: number
  /** 1-based init-container step currently running; only set while STARTING with an active init phase. */
  initStep?: number
  /** Total init containers; only set while the init phase is active. */
  initTotal?: number
  /** Short name of the running init step (init image basename without registry/tag). */
  initName?: string
}

export interface AppFileDto {
  path: string
  edited?: boolean
}

export interface RestartEventDto {
  ts: string
  app: string
  reason: string
  detail: string
  diagnostics?: string
}

export interface LinkDto {
  name: string
  url: string
  icon?: string
  order: number
  category?: string
  description?: string
  descriptionKey?: string
}

export interface NamespaceDto {
  id: string
  name: string
  status: string
  bundleRef: string
  bundleError?: string
  apps: AppDto[]
  links?: LinkDto[]
  // Host CPU core count from the daemon (runtime.NumCPU). Caps the aggregate
  // CPU progress bar at hostCpus*100% — Docker per-container stats span all
  // cores, so per-app 100% caps were wrong by a factor of N (N apps × 100
  // is unrelated to the host's actual capacity).
  hostCpus?: number
}

export interface HealthDto {
  status: string   // "healthy" | "degraded" | "unhealthy"
  healthy: boolean
  checks: HealthCheckDto[]
}

export interface HealthCheckDto {
  name: string
  status: string
  message: string
}

export interface DaemonStatusDto {
  running: boolean
  pid: number
  uptime: number
  version: string
  workspace: string
  socketPath: string
  desktop: boolean
  locale?: string
  theme?: string
}

export interface EventDto {
  type: string
  seq: number
  timestamp: number
  namespaceId: string
  appName: string
  before: string
  after: string
  /** Pull progress 0..100. Only present on `pull_progress` events. */
  percent?: number
  /** Human-readable progress phase ("Pulling: 234mb 50%"). Only present on `pull_progress`. */
  phase?: string
  /** 1-based progress index. Present on `snapshot_progress` (volume) and `app_init_step` (init step; absent = phase done). */
  current?: number
  /** Progress total. Present on `snapshot_progress` (volumes) and `app_init_step` (init containers). */
  total?: number
  /** Monitored filesystem path. Present on `disk_low` / `disk_ok` only. */
  path?: string
  /** Free bytes on the monitored filesystem. Present on `disk_low` / `disk_ok` only. */
  freeBytes?: number
  /** Low-disk threshold in bytes. Present on `disk_low` / `disk_ok` only. */
  thresholdBytes?: number
}

export interface AppInspectDto {
  name: string
  containerId: string
  image: string
  status: string
  state: string
  ports: string[] | null
  volumes: string[] | null
  env: string[] | null
  labels: Record<string, string> | null
  network: string
  restartCount: number
  startedAt: string
  uptime: number
}

export interface AppImageDto {
  ref: string
  present: boolean
  pulling?: boolean
  pullError?: string
  id?: string
  repoDigests?: string[]
  size?: number
  os?: string
  architecture?: string
  created?: string
}

// Phase E1: Welcome Screen
export interface NamespaceSummaryDto {
  id: string
  workspaceId: string
  name: string
  status: string
  bundleRef: string
}

export interface QuickStartDto {
  name: string
  template: string
  snapshot?: string
  // Resolved bundle ref ("repo:key") — Kotlin showed this as the QS button
  // subtitle. Falls back to template when the daemon couldn't resolve it.
  bundleRef?: string
}

// Phase E3: Namespace creation
export interface NamespaceCreateDto {
  name: string
  authType: string
  users?: string[]
  host: string
  port: number
  tlsEnabled: boolean
  tlsMode?: string
  pgAdminEnabled: boolean
  bundleRepo: string
  bundleKey: string
  workspaceId?: string
  snapshot?: string
  template?: string
  masterPassword?: string
  useDefaultPassword?: boolean
}

export interface BundleInfoDto {
  repo: string
  versions: string[]
}

// Phase F1: Secrets
export interface SecretMetaDto {
  id: string
  name: string
  type: string
  scope: string
  // The registry/git host this secret authenticates against; the host-filtered
  // picker uses it so a credential is reused per host. Optional — older daemons
  // and host-agnostic secrets omit it.
  host?: string
  createdAt: string
  // For BASIC_AUTH / REGISTRY_AUTH; surfaced so the write-only edit form can
  // prefill it. Optional — older daemons omit it.
  username?: string
}

export interface SecretCreateDto {
  id: string
  name: string
  type: string
  // For BASIC_AUTH / REGISTRY_AUTH only; mirrors Kotlin AuthSecret.Basic so
  // passwords containing ':' round-trip untouched.
  username?: string
  value: string
  scope?: string
  host?: string
}

// Write-only partial update (PUT /secrets/{id}). Empty/absent field = keep
// the existing value — in particular an empty `value` keeps the old secret
// value, so the edit form never needs to (and never does) display it.
export interface SecretUpdateDto {
  name?: string
  scope?: string
  username?: string
  value?: string
  host?: string
}

// Maps an image-registry host to a stored REGISTRY_AUTH secret for the active
// workspace (POST /registry-bindings). An empty secretId removes the binding.
export interface RegistryBindingDto {
  host: string
  secretId: string
}

// Phase F2: Diagnostics
export type DiagnosticsStatus = 'ok' | 'warn' | 'warning' | 'error'

export interface DiagnosticCheckDto {
  name: string
  status: DiagnosticsStatus
  message: string
  fixable: boolean
}

export interface DiagnosticsDto {
  checks: DiagnosticCheckDto[]
}

export interface DiagFixResultDto {
  fixed: number
  failed: number
  message: string
}

// Phase F3: Snapshots
export interface SnapshotDto {
  name: string
  createdAt: string
  size: number
}

// Multi-workspace (desktop-only). Endpoints return 404 in server mode.
//
// `repoPullPeriod` is an ISO 8601 duration string (e.g. "PT2H" = 2 hours).
// `authType` is "NONE" (default) or "TOKEN". With TOKEN the daemon resolves
// the repo token from `secretId` (a reusable GIT_TOKEN secret shared across
// workspaces) and falls back to the legacy per-workspace "ws:{id}:repo"
// secret when no secretId is linked. Optional fields on create/update get
// Kotlin-parity defaults when omitted.
export interface WorkspaceDto {
  id: string
  name: string
  repoUrl: string
  repoBranch: string
  repoPullPeriod?: string
  authType?: string
  /** Id of the linked GIT_TOKEN secret ('' / absent = none, legacy lookup). */
  secretId?: string
  active: boolean
  namespaces: number
}

export interface WorkspaceCreateDto {
  id?: string
  name: string
  repoUrl: string
  repoBranch?: string
  repoPullPeriod?: string
  authType?: string
  secretId?: string
}

// Partial-update semantics: absent field = unchanged. `secretId` keeps that
// rule and adds an explicit unlink sentinel: '' = unlink the secret.
export interface WorkspaceUpdateDto {
  name?: string
  repoUrl?: string
  repoBranch?: string
  repoPullPeriod?: string
  authType?: string
  secretId?: string
}

export interface UpdateStatusDto {
  currentVersion: string
  latestVersion?: string
  available: boolean
  lastCheckAt?: string
  error?: string
  applyError?: string
  applying: boolean
  /**
   * The last staging attempt hit a signature classification (the release has
   * no .sig, or it does not verify under the key this binary embeds — e.g.
   * after a signing-key rotation). Auto-install would keep failing; the UI
   * offers a calm manual-download path via releasesUrl instead.
   */
  manualUpdateRequired?: boolean
  manualUpdateReason?: 'signature_missing' | 'signature_mismatch' | string
  releasesUrl?: string
}

export interface ReleaseNoteDto {
  version: string
  date: string
  markdown: string
}

export interface AppConfigDto {
  content: string
  baseline: string
}

export interface AppFileContentDto {
  content: string
  baseline: string
}
