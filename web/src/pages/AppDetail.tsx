import { useEffect, useState, useCallback } from 'react'
import { useParams, Link } from 'react-router'
import { getAppInspect, getAppLogs, postAppRestart, getAppConfig, putAppConfig } from '../lib/api'
import type { AppInspectDto } from '../lib/types'
import { StatusBadge } from '../components/StatusBadge'
import { ConfirmModal } from '../components/ConfirmModal'
import { RotateCw, FileCode } from 'lucide-react'

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

  const load = useCallback(() => {
    if (!name) return
    getAppInspect(name).then(setInspect).catch((e) => setError(e.message))
    getAppLogs(name, 30).then(setLogs).catch(() => {})
    getAppConfig(name).then((y) => { setConfigYaml(y); setEditYaml(y) }).catch(() => {})
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

  if (!inspect) return <div className="text-muted-foreground text-xs p-3">Loading...</div>

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
      {inspect.volumes?.length > 0 && (
        <div className="rounded border border-border bg-card p-2">
          <div className="text-xs font-medium mb-1">Volumes</div>
          {inspect.volumes.map((v, i) => (
            <div key={i} className="text-[11px] font-mono text-muted-foreground break-all">{v}</div>
          ))}
        </div>
      )}

      {/* Environment */}
      {inspect.env?.length > 0 && (
        <div className="rounded border border-border bg-card p-2">
          <div className="text-xs font-medium mb-1">Environment</div>
          <div className="max-h-40 overflow-y-auto">
            {inspect.env.map((e, i) => (
              <div key={i} className="text-[11px] font-mono text-muted-foreground break-all">{e}</div>
            ))}
          </div>
        </div>
      )}

      {/* App config editor */}
      {configYaml !== null && (
        <div className="rounded border border-border bg-card p-2">
          <div className="flex items-center justify-between mb-1">
            <div className="flex items-center gap-1 text-xs font-medium">
              <FileCode size={13} /> App Config (YAML)
            </div>
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
