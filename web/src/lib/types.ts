export interface AppDto {
  name: string
  status: string
  image: string
  detached: boolean
  cpu: string
  memory: string
}

export interface NamespaceDto {
  id: string
  name: string
  status: string
  bundleRef: string
  apps: AppDto[]
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
