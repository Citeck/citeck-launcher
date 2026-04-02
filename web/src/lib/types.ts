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
  kind: string
  ports?: string[]
  edited?: boolean
  locked?: boolean
}

export interface LinkDto {
  name: string
  url: string
  icon?: string
  order: number
}

export interface NamespaceDto {
  id: string
  name: string
  status: string
  bundleRef: string
  bundleError?: string
  apps: AppDto[]
  links?: LinkDto[]
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
  value: string
  scope?: string
}

// Phase F2: Diagnostics
export interface DiagnosticCheckDto {
  name: string
  status: string
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
