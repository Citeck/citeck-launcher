import type { NamespaceDto, HealthDto, DaemonStatusDto, AppInspectDto, ActionResultDto } from './types'

const API_BASE = '/api/v1'

async function fetchJSON<T>(path: string): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    headers: { Accept: 'application/json' },
  })
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}: ${res.statusText}`)
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
  const res = await fetch(`${API_BASE}/apps/${name}/logs?tail=${tail}`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.text()
}

export async function getAppInspect(name: string): Promise<AppInspectDto> {
  return fetchJSON(`/apps/${name}/inspect`)
}

export async function postAppRestart(name: string): Promise<ActionResultDto> {
  const res = await fetch(`${API_BASE}/apps/${name}/restart`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function postAppStop(name: string): Promise<ActionResultDto> {
  const res = await fetch(`${API_BASE}/apps/${name}/stop`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function postAppStart(name: string): Promise<ActionResultDto> {
  const res = await fetch(`${API_BASE}/apps/${name}/start`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function postNamespaceStart(): Promise<ActionResultDto> {
  const res = await fetch(`${API_BASE}/namespace/start`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function postNamespaceStop(): Promise<ActionResultDto> {
  const res = await fetch(`${API_BASE}/namespace/stop`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function postNamespaceReload(): Promise<ActionResultDto> {
  const res = await fetch(`${API_BASE}/namespace/reload`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function getDaemonLogs(tail = 200): Promise<string> {
  const res = await fetch(`${API_BASE}/daemon/logs?tail=${tail}`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.text()
}

export async function getSystemDump(): Promise<void> {
  const res = await fetch(`${API_BASE}/system/dump`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = 'system-dump.json'
  a.click()
  URL.revokeObjectURL(url)
}

export async function getVolumes(): Promise<{ name: string; driver: string; mountpoint: string }[]> {
  return fetchJSON('/volumes')
}

export async function deleteVolume(name: string): Promise<ActionResultDto> {
  const res = await fetch(`${API_BASE}/volumes/${name}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function getAppConfig(name: string): Promise<string> {
  const res = await fetch(`${API_BASE}/apps/${name}/config`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.text()
}

export async function putAppConfig(name: string, content: string): Promise<ActionResultDto> {
  const res = await fetch(`${API_BASE}/apps/${name}/config`, {
    method: 'PUT',
    headers: { 'Content-Type': 'text/yaml' },
    body: content,
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ message: `HTTP ${res.status}` }))
    throw new Error(err.message || `HTTP ${res.status}`)
  }
  return res.json()
}

export async function getConfigContent(): Promise<string> {
  const res = await fetch(`${API_BASE}/config`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.text()
}

export async function putConfigContent(content: string): Promise<ActionResultDto> {
  const res = await fetch(`${API_BASE}/config`, {
    method: 'PUT',
    headers: { 'Content-Type': 'text/yaml' },
    body: content,
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ message: `HTTP ${res.status}` }))
    throw new Error(err.message || `HTTP ${res.status}`)
  }
  return res.json()
}
