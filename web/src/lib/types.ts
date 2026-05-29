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
  /** Per-volume index (1-based). Present on `snapshot_progress`. */
  current?: number
  /** Total volume count. Present on `snapshot_progress`. */
  total?: number
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

export interface TemplateDto {
  id: string
  name: string
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
  createdAt: string
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
// `authType` is "NONE" (default) or "TOKEN" (TOKEN resolves a secret stored
// under key "ws:{id}:repo"). Both fields are optional on create/update — the
// daemon applies Kotlin-parity defaults when omitted.
export interface WorkspaceDto {
  id: string
  name: string
  repoUrl: string
  repoBranch: string
  repoPullPeriod?: string
  authType?: string
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
}

export interface WorkspaceUpdateDto {
  name?: string
  repoUrl?: string
  repoBranch?: string
  repoPullPeriod?: string
  authType?: string
}
