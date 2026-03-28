import { useEffect, useState, useCallback } from 'react'
import { getConfigContent, putConfigContent, postNamespaceReload } from '../lib/api'
import { ConfirmModal } from './ConfirmModal'
import { YamlViewer } from './YamlViewer'
import { toast } from '../lib/toast'

interface ConfigEditorProps {
  compact?: boolean
}

export function ConfigEditor({ compact = false }: ConfigEditorProps) {
  const [configText, setConfigText] = useState<string | null>(null)
  const [editText, setEditText] = useState<string | null>(null)
  const [editing, setEditing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [showApplyConfirm, setShowApplyConfirm] = useState(false)

  const loadData = useCallback(async () => {
    setError(null)
    try {
      const cfg = await getConfigContent()
      setConfigText(cfg)
      setEditText(cfg)
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
    try {
      await putConfigContent(editText)
      await postNamespaceReload()
      setConfigText(editText)
      setEditing(false)
      toast('Configuration saved and reload requested', 'success')
      setShowApplyConfirm(false)
    } catch (err) {
      setError((err as Error).message)
      setShowApplyConfirm(false)
    } finally {
      setSaving(false)
    }
  }

  const hasChanges = editing && editText !== configText

  useEffect(() => {
    if (!hasChanges) return
    const handler = (e: BeforeUnloadEvent) => { e.preventDefault() }
    window.addEventListener('beforeunload', handler)
    return () => window.removeEventListener('beforeunload', handler)
  }, [hasChanges])

  return (
    <div className={compact ? 'flex flex-col h-full p-2' : 'space-y-3'}>
      {/* Feedback messages */}
      {error && (
        <div className={`rounded-lg border border-destructive/30 bg-destructive/5 text-sm text-destructive ${compact ? 'px-2 py-1 text-xs mb-1' : 'px-4 py-3'}`}>
          {error}
        </div>
      )}
      {/* Config editor */}
      <div className={compact ? 'flex flex-col flex-1 min-h-0' : 'rounded-lg border border-border bg-card p-4 space-y-3'}>
        <div className="flex items-center justify-between shrink-0">
          <h2 className={compact ? 'text-xs font-medium' : 'text-lg font-medium'}>namespace.yml</h2>
          <div className="flex items-center gap-2">
            {!editing ? (
              <button
                type="button"
                className="rounded-md border border-border px-3 py-1 text-xs font-medium hover:bg-muted"
                onClick={startEditing}
                disabled={!configText}
              >
                Edit
              </button>
            ) : (
              <>
                <button type="button" className="rounded-md border border-border px-3 py-1 text-xs font-medium hover:bg-muted"
                  onClick={cancelEditing}>Cancel</button>
                <button type="button"
                  className="rounded-md bg-primary px-3 py-1 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
                  onClick={() => setShowApplyConfirm(true)}
                  disabled={!hasChanges || saving}>
                  {saving ? 'Saving...' : 'Apply'}
                </button>
              </>
            )}
          </div>
        </div>

        {configText !== null ? (
          editing ? (
            <textarea
              className={`w-full rounded-md border border-border bg-background font-mono text-foreground focus:border-primary focus:outline-none ${compact ? 'flex-1 min-h-0 p-2 text-[11px] mt-1' : 'p-4 text-xs'}`}
              rows={compact ? undefined : Math.max(20, (editText ?? '').split('\n').length + 2)}
              value={editText ?? ''}
              onChange={(e) => setEditText(e.target.value)}
              spellCheck={false}
            />
          ) : (
            <div className={compact ? 'flex-1 min-h-0 overflow-auto mt-1' : ''}>
              <YamlViewer content={configText} />
            </div>
          )
        ) : error ? (
          <p className="text-sm text-destructive">
            Failed to load configuration: {error}
          </p>
        ) : (
          <p className="text-sm text-muted-foreground">
            No configuration file found. Use{' '}
            <code className="rounded bg-muted px-1 py-0.5 text-xs">citeck install</code> to create one.
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

