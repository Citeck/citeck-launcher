export interface ActionResultDto {
  success: boolean
  message: string
}

export interface AppDto {
  name: string
  status: string
  image: string
  cpu: string
  memory: string
  kind: string
  ports?: string[]
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
  apps: AppDto[]
  links?: LinkDto[]
}

export interface HealthDto {
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
}

export interface EventDto {
  type: string
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
  ports: string[]
  volumes: string[]
  env: string[]
  labels: Record<string, string>
  network: string
  restartCount: number
  startedAt: string
  uptime: number
}
