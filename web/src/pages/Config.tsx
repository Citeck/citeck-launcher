import { useEffect, useState, useCallback } from 'react'
import { Link } from 'react-router'
import { getHealth, getConfigContent, putConfigContent, postNamespaceReload } from '../lib/api'
import type { HealthDto } from '../lib/types'
import { ConfirmModal } from '../components/ConfirmModal'

export function Config() {
  const [health, setHealth] = useState<HealthDto | null>(null)
  const [configText, setConfigText] = useState<string | null>(null)
  const [editText, setEditText] = useState<string | null>(null)
  const [editing, setEditing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState<string | null>(null)
  const [showApplyConfirm, setShowApplyConfirm] = useState(false)

  const loadData = useCallback(async () => {
    setError(null)
    try {
      const [h, cfg] = await Promise.all([
        getHealth().catch(() => null),
        getConfigContent().catch(() => null),
      ])
      if (h) setHealth(h)
      if (cfg) {
        setConfigText(cfg)
        setEditText(cfg)
      }
    } catch (err) {
      setError((err as Error).message)
    }
  }, [])

  useEffect(() => {
    loadData()
  }, [loadData])

  function startEditing() {
    setEditText(configText)
    setEditing(true)
    setSuccess(null)
    setError(null)
  }

  function cancelEditing() {
    setEditing(false)
    setEditText(configText)
    setError(null)
  }

  async function handleApply() {
    if (!editText) return
    setSaving(true)
    setError(null)
    setSuccess(null)
    try {
      await putConfigContent(editText)
      await postNamespaceReload()
      setConfigText(editText)
      setEditing(false)
      setSuccess('Configuration saved and reload requested.')
      setShowApplyConfirm(false)
    } catch (err) {
      setError((err as Error).message)
      setShowApplyConfirm(false)
    } finally {
      setSaving(false)
    }
  }

  const hasChanges = editing && editText !== configText

  return (
    <div className="space-y-6">
      <div>
        <Link to="/" className="text-sm text-primary hover:underline">
          &larr; Back to dashboard
        </Link>
        <h1 className="text-2xl font-semibold mt-2">Configuration</h1>
      </div>

      {/* Health checks */}
      {health && (
        <div className="rounded-lg border border-border bg-card p-4 space-y-3">
          <h2 className="text-lg font-medium">System Health</h2>
          <div
            className={`rounded-md px-3 py-2 text-sm ${
              health.healthy
                ? 'bg-success/10 text-success border border-success/20'
                : 'bg-destructive/10 text-destructive border border-destructive/20'
            }`}
          >
            {health.healthy ? 'All systems healthy' : 'Issues detected'}
          </div>
          <div className="space-y-1">
            {health.checks.map((check) => (
              <div key={check.name} className="flex items-center gap-2 text-sm">
                <span
                  className={`inline-block h-2 w-2 rounded-full ${
                    check.status === 'ok'
                      ? 'bg-success'
                      : check.status === 'warning'
                        ? 'bg-warning'
                        : 'bg-destructive'
                  }`}
                />
                <span className="text-muted-foreground">{check.name}</span>
                <span>{check.message}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Feedback messages */}
      {error && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/5 px-4 py-3 text-sm text-destructive">
          {error}
        </div>
      )}
      {success && (
        <div className="rounded-lg border border-success/30 bg-success/5 px-4 py-3 text-sm text-success">
          {success}
        </div>
      )}

      {/* Config editor */}
      <div className="rounded-lg border border-border bg-card p-4 space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-medium">namespace.yml</h2>
          <div className="flex items-center gap-2">
            {!editing ? (
              <button
                type="button"
                className="rounded-md border border-border px-3 py-1.5 text-xs font-medium hover:bg-muted"
                onClick={startEditing}
                disabled={!configText}
              >
                Edit
              </button>
            ) : (
              <>
                <button
                  type="button"
                  className="rounded-md border border-border px-3 py-1.5 text-xs font-medium hover:bg-muted"
                  onClick={cancelEditing}
                >
                  Cancel
                </button>
                <button
                  type="button"
                  className="rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
                  onClick={() => setShowApplyConfirm(true)}
                  disabled={!hasChanges || saving}
                >
                  {saving ? 'Saving...' : 'Apply'}
                </button>
              </>
            )}
          </div>
        </div>

        {configText !== null ? (
          editing ? (
            <textarea
              className="w-full rounded-md border border-border bg-background p-4 font-mono text-xs text-foreground focus:border-primary focus:outline-none"
              rows={Math.max(20, (editText ?? '').split('\n').length + 2)}
              value={editText ?? ''}
              onChange={(e) => setEditText(e.target.value)}
              spellCheck={false}
            />
          ) : (
            <YamlViewer content={configText} />
          )
        ) : (
          <p className="text-sm text-muted-foreground">
            No configuration file found. Use{' '}
            <code className="rounded bg-muted px-1 py-0.5 text-xs">citeck install</code> to create
            one.
          </p>
        )}
      </div>

      <ConfirmModal
        open={showApplyConfirm}
        title="Apply Configuration"
        message="Save the configuration and reload the namespace? Running apps may be restarted."
        confirmLabel="Apply"
        loading={saving}
        onConfirm={handleApply}
        onCancel={() => setShowApplyConfirm(false)}
      />
    </div>
  )
}

function YamlViewer({ content }: { content: string }) {
  return (
    <pre className="rounded-md bg-background p-4 text-xs font-mono overflow-x-auto leading-relaxed">
      {content.split('\n').map((line, i) => (
        <YamlLine key={i} line={line} />
      ))}
    </pre>
  )
}

function YamlLine({ line }: { line: string }) {
  if (line.trim() === '') return <span>{'\n'}</span>

  // Comment
  if (line.trimStart().startsWith('#')) {
    return (
      <span>
        <span className="text-muted-foreground">{line}</span>
        {'\n'}
      </span>
    )
  }

  // Key: value
  const match = line.match(/^(\s*)([\w.-]+)(:)(.*)$/)
  if (match) {
    const [, indent, key, colon, rest] = match
    return (
      <span>
        {indent}
        <span className="text-primary">{key}</span>
        <span className="text-muted-foreground">{colon}</span>
        <YamlValue value={rest} />
        {'\n'}
      </span>
    )
  }

  // List item
  const listMatch = line.match(/^(\s*)(- )(.*)$/)
  if (listMatch) {
    const [, indent, dash, value] = listMatch
    return (
      <span>
        {indent}
        <span className="text-warning">{dash}</span>
        <YamlValue value={value} />
        {'\n'}
      </span>
    )
  }

  return (
    <span>
      {line}
      {'\n'}
    </span>
  )
}

function YamlValue({ value }: { value: string }) {
  const trimmed = value.trimStart()
  if (trimmed === '' || trimmed === '~' || trimmed === 'null') {
    return <span className="text-muted-foreground">{value}</span>
  }
  if (trimmed === 'true' || trimmed === 'false') {
    return <span className="text-warning">{value}</span>
  }
  if (/^\s*\d+(\.\d+)?$/.test(value)) {
    return <span className="text-success">{value}</span>
  }
  return <span className="text-foreground">{value}</span>
}
