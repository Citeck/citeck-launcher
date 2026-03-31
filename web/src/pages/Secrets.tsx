import { useEffect, useState, useCallback } from 'react'
import { getSecrets, createSecret, deleteSecret, testSecret, getSecretsStatus, setupSecretsPassword } from '../lib/api'
import type { SecretMetaDto } from '../lib/types'
import { ConfirmModal } from '../components/ConfirmModal'
import { toast } from '../lib/toast'
import { useTranslation } from '../lib/i18n'
import { Trash2, Plus, FlaskConical, CheckCircle, XCircle, KeyRound, Lock, ShieldCheck } from 'lucide-react'

interface SecretFormData {
  id: string
  name: string
  type: string
  value: string
}

const SECRET_TYPES = ['GIT_TOKEN', 'BASIC_AUTH', 'REGISTRY_AUTH'] as const

const emptyForm: SecretFormData = { id: '', name: '', type: 'GIT_TOKEN', value: '' }

export function Secrets() {
  const { t } = useTranslation()
  const [secrets, setSecrets] = useState<SecretMetaDto[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState<SecretFormData>(emptyForm)
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const [testResult, setTestResult] = useState<Record<string, 'ok' | 'fail'>>({})
  const [encStatus, setEncStatus] = useState<{ encrypted: boolean; locked: boolean } | null>(null)
  const [showSetPwd, setShowSetPwd] = useState(false)
  const [setPwd, setSetPwd] = useState('')
  const [setPwdLoading, setSetPwdLoading] = useState(false)
  const [setPwdError, setSetPwdError] = useState<string | null>(null)

  const loadSecrets = useCallback(() => {
    setLoading(true)
    getSecrets().then(setSecrets).catch((e) => setError(e.message)).finally(() => setLoading(false))
  }, [])

  useEffect(() => { loadSecrets() }, [loadSecrets])

  useEffect(() => {
    getSecretsStatus().then(setEncStatus).catch(() => {})
  }, [])

  async function handleSetPassword(e: React.FormEvent) {
    e.preventDefault()
    if (!setPwd) return
    setSetPwdLoading(true)
    setSetPwdError(null)
    try {
      await setupSecretsPassword(setPwd)
      setShowSetPwd(false)
      setSetPwd('')
      setEncStatus({ encrypted: true, locked: false })
      toast(t('secrets.encrypted.success'), 'success')
    } catch (err) {
      setSetPwdError((err as Error).message)
    } finally {
      setSetPwdLoading(false)
    }
  }

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
      toast(t('secrets.create.success'), 'success')
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
      toast(t('secrets.delete.success'), 'success')
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
      {loading && (
        <div className="space-y-3 mb-4">
          <div className="h-5 w-24 bg-muted rounded animate-pulse" />
          {Array.from({ length: 3 }).map((_, i) => (
            <div key={i} className="h-6 w-full bg-muted rounded animate-pulse" />
          ))}
        </div>
      )}
      <div className="flex items-center justify-between mb-2">
        <h1 className="text-base font-semibold flex items-center gap-1.5">
          <KeyRound size={16} />
          {t('secrets.title')}
          {encStatus?.encrypted && !encStatus.locked && (
            <ShieldCheck size={14} className="text-green-500" title={t('secrets.encrypted.badge')} />
          )}
          {encStatus?.locked && (
            <Lock size={14} className="text-yellow-500" title={t('secrets.locked')} />
          )}
        </h1>
        <div className="flex items-center gap-2">
          {encStatus && !encStatus.encrypted && secrets.length > 0 && (
            <button
              type="button"
              className="flex items-center gap-1 rounded-md border border-border px-2.5 py-1 text-xs hover:bg-muted"
              onClick={() => { setShowSetPwd(!showSetPwd); setSetPwdError(null) }}
            >
              <Lock size={13} />
              {t('secrets.setPassword')}
            </button>
          )}
          <button
            type="button"
            className="flex items-center gap-1 rounded-md border border-border px-2.5 py-1 text-xs hover:bg-muted"
            onClick={() => { setShowForm(!showForm); setCreateError(null) }}
          >
            <Plus size={13} />
            {t('secrets.add')}
          </button>
        </div>
      </div>

      {encStatus?.locked && (
        <div className="flex items-center gap-2 mb-2 p-2 rounded border border-yellow-500/30 bg-yellow-500/5 text-xs text-yellow-500">
          <Lock size={13} />
          {t('secrets.locked')}
        </div>
      )}

      {showSetPwd && (
        <form onSubmit={handleSetPassword} className="mb-3 rounded border border-border bg-card p-3 space-y-2">
          <p className="text-xs text-muted-foreground">{t('secrets.setPassword.description')}</p>
          <input
            type="password"
            className="w-full rounded border border-border bg-background px-2 py-1 text-xs focus:outline-none focus:border-primary"
            placeholder={t('migration.passwordPlaceholder')}
            value={setPwd}
            onChange={(e) => setSetPwd(e.target.value)}
            autoFocus
          />
          {setPwdError && <div className="text-destructive text-xs">{setPwdError}</div>}
          <div className="flex gap-2">
            <button
              type="submit"
              disabled={setPwdLoading || !setPwd}
              className="rounded-md bg-primary text-primary-foreground px-3 py-1 text-xs font-medium hover:bg-primary/90 disabled:opacity-50"
            >
              {setPwdLoading ? '...' : t('migration.confirm')}
            </button>
            <button
              type="button"
              className="rounded-md border border-border px-3 py-1 text-xs hover:bg-muted"
              onClick={() => { setShowSetPwd(false); setSetPwdError(null) }}
            >
              {t('secrets.form.cancel')}
            </button>
          </div>
        </form>
      )}

      {error && <div className="text-destructive text-xs mb-2">{error}</div>}

      {showForm && (
        <form onSubmit={handleCreate} className="mb-3 rounded border border-border bg-card p-3 space-y-2">
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="block text-[11px] text-muted-foreground mb-0.5">{t('secrets.form.id')}</label>
              <input
                type="text"
                className="w-full rounded border border-border bg-background px-2 py-1 text-xs focus:outline-none focus:border-primary"
                value={form.id}
                onChange={(e) => setForm({ ...form, id: e.target.value })}
                placeholder={t('secrets.form.id.placeholder')}
                required
              />
            </div>
            <div>
              <label className="block text-[11px] text-muted-foreground mb-0.5">{t('secrets.form.name')}</label>
              <input
                type="text"
                className="w-full rounded border border-border bg-background px-2 py-1 text-xs focus:outline-none focus:border-primary"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder={t('secrets.form.name.placeholder')}
                required
              />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="block text-[11px] text-muted-foreground mb-0.5">{t('secrets.form.type')}</label>
              <select
                className="w-full rounded border border-border bg-background px-2 py-1 text-xs focus:outline-none focus:border-primary"
                value={form.type}
                onChange={(e) => setForm({ ...form, type: e.target.value })}
              >
                {SECRET_TYPES.map((st) => (
                  <option key={st} value={st}>{st}</option>
                ))}
              </select>
            </div>
            <div>
              <label className="block text-[11px] text-muted-foreground mb-0.5">{t('secrets.form.value')}</label>
              <input
                type="password"
                className="w-full rounded border border-border bg-background px-2 py-1 text-xs focus:outline-none focus:border-primary"
                value={form.value}
                onChange={(e) => setForm({ ...form, value: e.target.value })}
                placeholder={t('secrets.form.value.placeholder')}
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
              {creating ? t('secrets.form.creating') : t('secrets.form.create')}
            </button>
            <button
              type="button"
              className="rounded-md border border-border px-3 py-1 text-xs hover:bg-muted"
              onClick={() => { setShowForm(false); setCreateError(null) }}
            >
              {t('secrets.form.cancel')}
            </button>
          </div>
        </form>
      )}

      <table className="w-full text-xs border-collapse">
        <thead>
          <tr className="text-left text-muted-foreground border-b border-border">
            <th className="py-1 pr-4 font-medium">{t('secrets.table.name')}</th>
            <th className="py-1 pr-4 font-medium">{t('secrets.table.type')}</th>
            <th className="py-1 pr-4 font-medium">{t('secrets.table.scope')}</th>
            <th className="py-1 pr-4 font-medium">{t('secrets.table.created')}</th>
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
                  title={t('secrets.test.tooltip')}
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
                  title={t('common.delete')}
                  onClick={() => setDeleteTarget(s.id)}
                >
                  <Trash2 size={14} />
                </button>
              </td>
            </tr>
          ))}
          {secrets.length === 0 && (
            <tr><td colSpan={5} className="py-4 text-center text-muted-foreground">{t('secrets.empty')}</td></tr>
          )}
        </tbody>
      </table>

      <ConfirmModal
        open={!!deleteTarget}
        title={t('secrets.delete.title')}
        message={t('secrets.delete.message', { name: secrets.find((s) => s.id === deleteTarget)?.name ?? deleteTarget ?? '' })}
        confirmLabel={t('common.delete')}
        confirmVariant="danger"
        loading={deleting}
        error={deleteError}
        onConfirm={handleDelete}
        onCancel={() => { setDeleteTarget(null); setDeleteError(null) }}
      />
    </div>
  )
}
