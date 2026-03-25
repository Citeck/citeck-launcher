import { useEffect, useState, useCallback } from 'react'
import { getSecrets, createSecret, deleteSecret, testSecret } from '../lib/api'
import type { SecretMetaDto } from '../lib/types'
import { ConfirmModal } from '../components/ConfirmModal'
import { Trash2, Plus, FlaskConical, CheckCircle, XCircle, KeyRound } from 'lucide-react'

interface SecretFormData {
  id: string
  name: string
  type: string
  value: string
}

const SECRET_TYPES = ['GIT_TOKEN', 'BASIC_AUTH', 'REGISTRY_AUTH'] as const

const emptyForm: SecretFormData = { id: '', name: '', type: 'GIT_TOKEN', value: '' }

export function Secrets() {
  const [secrets, setSecrets] = useState<SecretMetaDto[]>([])
  const [error, setError] = useState<string | null>(null)
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState<SecretFormData>(emptyForm)
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const [testResult, setTestResult] = useState<Record<string, 'ok' | 'fail'>>({})

  const loadSecrets = useCallback(() => {
    getSecrets().then(setSecrets).catch((e) => setError(e.message))
  }, [])

  useEffect(() => { loadSecrets() }, [loadSecrets])

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault()
    if (!form.id || !form.name || !form.value) return
    setCreating(true)
    setCreateError(null)
    try {
      await createSecret({ id: form.id, name: form.name, type: form.type, value: form.value })
      setForm(emptyForm)
      setShowForm(false)
      loadSecrets()
    } catch (err) {
      setCreateError((err as Error).message)
    } finally {
      setCreating(false)
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return
    setDeleting(true)
    setDeleteError(null)
    try {
      await deleteSecret(deleteTarget)
      setDeleteTarget(null)
      loadSecrets()
    } catch (err) {
      setDeleteError((err as Error).message)
    } finally {
      setDeleting(false)
    }
  }

  async function handleTest(id: string) {
    try {
      const result = await testSecret(id)
      setTestResult((prev) => ({ ...prev, [id]: result.success ? 'ok' : 'fail' }))
    } catch {
      setTestResult((prev) => ({ ...prev, [id]: 'fail' }))
    }
  }

  return (
    <div className="p-3">
      <div className="flex items-center justify-between mb-2">
        <h1 className="text-base font-semibold flex items-center gap-1.5">
          <KeyRound size={16} />
          Secrets
        </h1>
        <button
          type="button"
          className="flex items-center gap-1 rounded-md border border-border px-2.5 py-1 text-xs hover:bg-muted"
          onClick={() => { setShowForm(!showForm); setCreateError(null) }}
        >
          <Plus size={13} />
          Add Secret
        </button>
      </div>

      {error && <div className="text-destructive text-xs mb-2">{error}</div>}

      {showForm && (
        <form onSubmit={handleCreate} className="mb-3 rounded border border-border bg-card p-3 space-y-2">
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="block text-[11px] text-muted-foreground mb-0.5">ID</label>
              <input
                type="text"
                className="w-full rounded border border-border bg-background px-2 py-1 text-xs focus:outline-none focus:border-primary"
                value={form.id}
                onChange={(e) => setForm({ ...form, id: e.target.value })}
                placeholder="unique-id"
                required
              />
            </div>
            <div>
              <label className="block text-[11px] text-muted-foreground mb-0.5">Name</label>
              <input
                type="text"
                className="w-full rounded border border-border bg-background px-2 py-1 text-xs focus:outline-none focus:border-primary"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="My Secret"
                required
              />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="block text-[11px] text-muted-foreground mb-0.5">Type</label>
              <select
                className="w-full rounded border border-border bg-background px-2 py-1 text-xs focus:outline-none focus:border-primary"
                value={form.type}
                onChange={(e) => setForm({ ...form, type: e.target.value })}
              >
                {SECRET_TYPES.map((t) => (
                  <option key={t} value={t}>{t}</option>
                ))}
              </select>
            </div>
            <div>
              <label className="block text-[11px] text-muted-foreground mb-0.5">Value</label>
              <input
                type="password"
                className="w-full rounded border border-border bg-background px-2 py-1 text-xs focus:outline-none focus:border-primary"
                value={form.value}
                onChange={(e) => setForm({ ...form, value: e.target.value })}
                placeholder="secret value"
                required
              />
            </div>
          </div>
          {createError && <div className="text-destructive text-xs">{createError}</div>}
          <div className="flex gap-2">
            <button
              type="submit"
              disabled={creating}
              className="rounded-md bg-primary text-primary-foreground px-3 py-1 text-xs font-medium hover:bg-primary/90 disabled:opacity-50"
            >
              {creating ? 'Creating...' : 'Create'}
            </button>
            <button
              type="button"
              className="rounded-md border border-border px-3 py-1 text-xs hover:bg-muted"
              onClick={() => { setShowForm(false); setCreateError(null) }}
            >
              Cancel
            </button>
          </div>
        </form>
      )}

      <table className="w-full text-xs border-collapse">
        <thead>
          <tr className="text-left text-muted-foreground border-b border-border">
            <th className="py-1 pr-4 font-medium">Name</th>
            <th className="py-1 pr-4 font-medium">Type</th>
            <th className="py-1 pr-4 font-medium">Scope</th>
            <th className="py-1 pr-4 font-medium">Created</th>
            <th className="py-1 font-medium text-right w-24"></th>
          </tr>
        </thead>
        <tbody>
          {secrets.map((s) => (
            <tr key={s.id} className="border-b border-border/20 hover:bg-muted/30">
              <td className="py-[3px] pr-4 font-mono">{s.name}</td>
              <td className="py-[3px] pr-4 text-muted-foreground">{s.type}</td>
              <td className="py-[3px] pr-4 text-muted-foreground">{s.scope}</td>
              <td className="py-[3px] pr-4 text-muted-foreground">{new Date(s.createdAt).toLocaleDateString()}</td>
              <td className="py-[3px] text-right flex items-center justify-end gap-1">
                <button
                  type="button"
                  className="p-1 rounded text-muted-foreground hover:text-primary hover:bg-muted"
                  title="Test secret"
                  onClick={() => handleTest(s.id)}
                >
                  {testResult[s.id] === 'ok' ? (
                    <CheckCircle size={14} className="text-green-500" />
                  ) : testResult[s.id] === 'fail' ? (
                    <XCircle size={14} className="text-destructive" />
                  ) : (
                    <FlaskConical size={14} />
                  )}
                </button>
                <button
                  type="button"
                  className="p-1 rounded text-muted-foreground hover:text-destructive hover:bg-muted"
                  title="Delete secret"
                  onClick={() => setDeleteTarget(s.id)}
                >
                  <Trash2 size={14} />
                </button>
              </td>
            </tr>
          ))}
          {secrets.length === 0 && (
            <tr><td colSpan={5} className="py-4 text-center text-muted-foreground">No secrets configured</td></tr>
          )}
        </tbody>
      </table>

      <ConfirmModal
        open={!!deleteTarget}
        title={`Delete secret?`}
        message={`This will permanently delete the secret "${secrets.find((s) => s.id === deleteTarget)?.name ?? deleteTarget}". Any namespaces using it will lose access.`}
        confirmLabel="Delete"
        confirmVariant="danger"
        loading={deleting}
        error={deleteError}
        onConfirm={handleDelete}
        onCancel={() => { setDeleteTarget(null); setDeleteError(null) }}
      />
    </div>
  )
}
