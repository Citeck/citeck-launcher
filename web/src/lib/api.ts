import type {
  NamespaceDto,
  HealthDto,
  DaemonStatusDto,
  AppInspectDto,
  ActionResultDto,
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
} from './types'

const API_BASE = '/api/v1'

const CSRF_HEADER = { 'X-Citeck-CSRF': '1' }

async function extractErrorMessage(res: Response): Promise<string> {
  try {
    const body = await res.json()
    if (body.message) return body.message
  } catch { /* not JSON, use statusText */ }
  return res.statusText || `HTTP ${res.status}`
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

export async function postNamespaceStart(): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/namespace/start`, { method: 'POST', headers: CSRF_HEADER })
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

export async function getVolumes(): Promise<{ name: string; path: string }[]> {
  return fetchJSON('/volumes')
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

export async function getAppFiles(name: string): Promise<string[]> {
  return fetchJSON<string[]>(`/apps/${name}/files`)
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
  if (!res.ok) throw new Error(await extractErrorMessage(res))
  return res.json()
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

export async function postExportSnapshot(): Promise<ActionResultDto> {
  const res = await fetchWithTimeout(`${API_BASE}/snapshots/export`, { method: 'POST', headers: CSRF_HEADER }, 300_000)
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
