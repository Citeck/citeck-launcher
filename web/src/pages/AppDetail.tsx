import { useEffect, useState, useCallback } from 'react'
import { useParams, Link } from 'react-router'
import { getAppInspect, getAppLogs, postAppRestart, getAppConfig, putAppConfig, putAppLock, getAppFiles, getAppFile, putAppFile } from '../lib/api'
import type { AppInspectDto } from '../lib/types'
import { StatusBadge } from '../components/StatusBadge'
import { ConfirmModal } from '../components/ConfirmModal'
import { useDashboardStore } from '../lib/store'
import { RotateCw, FileCode, Lock, Unlock } from 'lucide-react'

function formatUptime(ms: number): string {
  if (ms <= 0) return '—'
  const s = Math.floor(ms / 1000), m = Math.floor(s / 60), h = Math.floor(m / 60), d = Math.floor(h / 24)
  if (d > 0) return `${d}d ${h % 24}h ${m % 60}m`
  if (h > 0) return `${h}h ${m % 60}m ${s % 60}s`
  if (m > 0) return `${m}m ${s % 60}s`
  return `${s}s`
}

export function AppDetail() {
  const { name } = useParams<{ name: string }>()
  const [inspect, setInspect] = useState<AppInspectDto | null>(null)
  const [logs, setLogs] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [restarting, setRestarting] = useState(false)
  // Config editor
  const [configYaml, setConfigYaml] = useState<string | null>(null)
  const [editYaml, setEditYaml] = useState<string | null>(null)
  const [configEditing, setConfigEditing] = useState(false)
  const [configSaving, setConfigSaving] = useState(false)
  const [configError, setConfigError] = useState<string | null>(null)
  const [showApplyConfirm, setShowApplyConfirm] = useState(false)
  // File editor
  const [files, setFiles] = useState<string[]>([])
  const [editingFile, setEditingFile] = useState<string | null>(null)
  const [fileContent, setFileContent] = useState('')
  const [fileSaving, setFileSaving] = useState(false)
  const [fileError, setFileError] = useState<string | null>(null)
  const nsApps = useDashboardStore((s) => s.namespace?.apps)
  const appMeta = nsApps?.find((a) => a.name === name)
  const isEdited = appMeta?.edited ?? false
  const isLocked = appMeta?.locked ?? false

  const load = useCallback(() => {
    if (!name) return
    getAppInspect(name).then(setInspect).catch((e) => setError(e.message))
    getAppLogs(name, 30).then(setLogs).catch(() => setLogs('(logs unavailable)'))
    getAppConfig(name).then((y) => { setConfigYaml(y); setEditYaml(y) }).catch(() => {})
    getAppFiles(name).then(setFiles).catch(() => {})
  }, [name])

  useEffect(() => { load() }, [load])

  const handleRestart = async () => {
    if (!name) return
    setRestarting(true)
    try { await postAppRestart(name) } finally { setRestarting(false) }
  }

  async function handleApplyConfig() {
    if (!name || !editYaml) return
    setConfigSaving(true); setConfigError(null)
    try {
      await putAppConfig(name, editYaml)
      setConfigYaml(editYaml)
      setConfigEditing(false)
      setShowApplyConfirm(false)
    } catch (e) {
      setConfigError((e as Error).message)
    } finally {
      setConfigSaving(false)
    }
  }

  if (error) return (
    <div className="p-3">
      <Link to="/" className="text-xs text-primary hover:underline">&larr; Dashboard</Link>
      <div className="text-destructive text-xs mt-2">Error: {error}</div>
    </div>
  )

  if (!inspect) return (
    <div className="p-3 space-y-3">
      <div className="flex items-center gap-2">
        <div className="h-4 w-6 bg-muted rounded animate-pulse" />
        <div className="h-5 w-32 bg-muted rounded animate-pulse" />
        <div className="h-4 w-16 bg-muted rounded animate-pulse" />
      </div>
      <div className="rounded border border-border bg-card p-2 space-y-1.5">
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="h-3 w-full bg-muted rounded animate-pulse" />
        ))}
      </div>
      <div className="rounded border border-border bg-card p-2 space-y-1.5">
        <div className="h-3 w-24 bg-muted rounded animate-pulse" />
        {Array.from({ length: 3 }).map((_, i) => (
          <div key={i} className="h-3 w-full bg-muted rounded animate-pulse" />
        ))}
      </div>
    </div>
  )

  return (
    <div className="p-3 space-y-3">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Link to="/" className="text-xs text-primary hover:underline">&larr;</Link>
          <h1 className="text-base font-semibold">{inspect.name}</h1>
          <StatusBadge status={inspect.status} />
        </div>
        <button onClick={handleRestart} disabled={restarting}
          className="flex items-center gap-1 rounded border border-border px-2 py-1 text-xs text-muted-foreground hover:text-foreground hover:bg-muted disabled:opacity-50">
          <RotateCw size={12} /> {restarting ? 'Restarting...' : 'Restart'}
        </button>
      </div>

      {/* Details grid */}
      <div className="grid grid-cols-[auto_1fr_auto_1fr] gap-x-4 gap-y-0.5 text-xs rounded border border-border bg-card p-2">
        <D l="Container" v={inspect.containerId?.slice(0, 12) || '—'} />
        <D l="Image" v={inspect.image} />
        <D l="State" v={inspect.state} />
        <D l="Network" v={inspect.network} />
        <D l="Started" v={inspect.startedAt ? new Date(inspect.startedAt).toLocaleString() : '—'} />
        <D l="Uptime" v={formatUptime(inspect.uptime)} />
        <D l="Restarts" v={String(inspect.restartCount)} />
        <D l="Ports" v={inspect.ports?.join(', ') || '—'} />
      </div>

      {/* Volumes */}
      {(inspect.volumes?.length ?? 0) > 0 && (
        <div className="rounded border border-border bg-card p-2">
          <div className="text-xs font-medium mb-1">Volumes</div>
          {inspect.volumes!.map((v, i) => (
            <div key={i} className="text-[11px] font-mono text-muted-foreground break-all">{v}</div>
          ))}
        </div>
      )}

      {/* Environment */}
      {(inspect.env?.length ?? 0) > 0 && (
        <div className="rounded border border-border bg-card p-2">
          <div className="text-xs font-medium mb-1">Environment</div>
          <div className="max-h-40 overflow-y-auto">
            {inspect.env!.map((e, i) => {
              const isMasked = e.endsWith('=***')
              return (
                <div key={i} className="text-[11px] font-mono overflow-hidden text-ellipsis whitespace-nowrap" title={e}>
                  {isMasked ? (
                    <><span className="text-muted-foreground">{e.slice(0, e.length - 3)}</span><span className="text-muted-foreground/50">***</span></>
                  ) : (
                    <span className="text-muted-foreground">{e}</span>
                  )}
                </div>
              )
            })}
          </div>
        </div>
      )}

      {/* App config editor */}
      {configYaml !== null && (
        <div className="rounded border border-border bg-card p-2">
          <div className="flex items-center justify-between mb-1">
            <div className="flex items-center gap-1 text-xs font-medium">
              <FileCode size={13} /> App Config (YAML)
              {isEdited && <span className="text-[10px] text-blue-500 font-normal ml-1">(edited{isLocked ? ', locked' : ''})</span>}
            </div>
            <div className="flex items-center gap-1">
              {isEdited && (
                <button type="button"
                  className={`flex items-center gap-0.5 rounded border border-border px-2 py-0.5 text-xs hover:bg-muted ${isLocked ? 'text-blue-500' : 'text-muted-foreground'}`}
                  title={isLocked ? 'Unlock: edits will NOT survive regeneration' : 'Lock: edits survive regeneration'}
                  onClick={() => putAppLock(name!, !isLocked).catch((e) => setConfigError((e as Error).message))}>
                  {isLocked ? <Lock size={11} /> : <Unlock size={11} />}
                  {isLocked ? 'Locked' : 'Unlocked'}
                </button>
              )}
            {!configEditing ? (
              <button type="button" className="rounded border border-border px-2 py-0.5 text-xs hover:bg-muted"
                onClick={() => { setEditYaml(configYaml); setConfigEditing(true); setConfigError(null) }}>
                Edit
              </button>
            ) : (
              <div className="flex gap-1">
                <button type="button" className="rounded border border-border px-2 py-0.5 text-xs hover:bg-muted"
                  onClick={() => setConfigEditing(false)}>Cancel</button>
                <button type="button" className="rounded bg-primary px-2 py-0.5 text-xs text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
                  onClick={() => setShowApplyConfirm(true)} disabled={editYaml === configYaml}>Apply</button>
              </div>
            )}
            </div>
          </div>
          {configError && <div className="text-[11px] text-destructive mb-1">{configError}</div>}
          {configEditing ? (
            <textarea className="w-full rounded border border-border bg-background p-2 font-mono text-[11px] text-foreground focus:border-primary focus:outline-none"
              rows={Math.max(10, (editYaml ?? '').split('\n').length + 1)}
              value={editYaml ?? ''} onChange={(e) => setEditYaml(e.target.value)} spellCheck={false} />
          ) : (
            <pre className="max-h-48 overflow-auto rounded bg-background p-2 text-[11px] font-mono text-muted-foreground whitespace-pre-wrap">{configYaml}</pre>
          )}
        </div>
      )}

      {/* Mounted Files */}
      {files.length > 0 && (
        <div className="rounded border border-border bg-card p-2">
          <div className="text-xs font-medium mb-1">Mounted Files</div>
          {files.map((f) => (
            <div key={f} className="flex items-center gap-2 text-[11px] font-mono">
              <span className="text-muted-foreground flex-1 break-all">{f}</span>
              <button type="button" className="text-primary hover:underline text-[10px] shrink-0"
                onClick={async () => {
                  try {
                    const content = await getAppFile(name!, f)
                    setEditingFile(f); setFileContent(content); setFileError(null)
                  } catch (e) { setFileError((e as Error).message) }
                }}>Edit</button>
            </div>
          ))}
          {fileError && !editingFile && <div className="text-[10px] text-destructive mt-1">{fileError}</div>}
          {editingFile && (
            <div className="mt-2 border-t border-border pt-2">
              <div className="flex items-center justify-between mb-1">
                <span className="text-[11px] font-mono text-muted-foreground">{editingFile}</span>
                <div className="flex gap-1">
                  <button type="button" className="rounded border border-border px-2 py-0.5 text-xs hover:bg-muted"
                    onClick={() => { setEditingFile(null); setFileError(null) }}>Cancel</button>
                  <button type="button" className="rounded bg-primary px-2 py-0.5 text-xs text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
                    disabled={fileSaving}
                    onClick={async () => {
                      setFileSaving(true); setFileError(null)
                      try {
                        await putAppFile(name!, editingFile, fileContent)
                        setEditingFile(null)
                      } catch (e) { setFileError((e as Error).message) }
                      finally { setFileSaving(false) }
                    }}>{fileSaving ? 'Saving...' : 'Save'}</button>
                </div>
              </div>
              {fileError && <div className="text-[10px] text-destructive mb-1">{fileError}</div>}
              <textarea className="w-full rounded border border-border bg-background p-2 font-mono text-[11px] text-foreground focus:border-primary focus:outline-none"
                rows={Math.max(8, fileContent.split('\n').length + 1)}
                value={fileContent} onChange={(e) => setFileContent(e.target.value)} spellCheck={false} />
            </div>
          )}
        </div>
      )}

      {/* Logs */}
      {logs && (
        <div className="rounded border border-border bg-card p-2">
          <div className="flex items-center justify-between mb-1">
            <div className="text-xs font-medium">Recent Logs</div>
            <Link to={`/apps/${name}/logs`} className="text-xs text-primary hover:underline">Full logs &rarr;</Link>
          </div>
          <pre className="max-h-32 overflow-y-auto rounded bg-background p-2 text-[11px] font-mono text-foreground whitespace-pre-wrap">{logs}</pre>
        </div>
      )}

      <ConfirmModal open={showApplyConfirm} title="Apply config changes?"
        message="Save config and restart the app?"
        confirmLabel="Apply" loading={configSaving}
        onConfirm={handleApplyConfig} onCancel={() => setShowApplyConfirm(false)} />
    </div>
  )
}

function D({ l, v }: { l: string; v: string }) {
  return <>
    <span className="text-muted-foreground">{l}</span>
    <span className="font-mono truncate" title={v}>{v}</span>
  </>
}
