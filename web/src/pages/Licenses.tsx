import { useEffect, useState, useCallback } from 'react'
import { getLicenses, createLicense, deleteLicense, type LicenseDto } from '../lib/api'
import { ConfirmModal } from '../components/ConfirmModal'
import { toast } from '../lib/toast'
import { useTranslation } from '../lib/i18n'
import { Trash2, Plus, BadgeCheck, BadgeX } from 'lucide-react'

/**
 * Licenses screen — enterprise license management (matches Kotlin
 * LicenseService UI). Licenses are stored as encrypted secrets via the
 * SecretService so they survive launcher upgrades and ride along with the
 * existing master-password unlock flow.
 *
 * The "add" form expects raw signed-license JSON (the format produced by the
 * Kotlin signing pipeline) — we don't have UI for crafting a license, the
 * tenant operator pastes the JSON they received from Citeck.
 */
export function Licenses() {
  const { t } = useTranslation()
  const [licenses, setLicenses] = useState<LicenseDto[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [showForm, setShowForm] = useState(false)
  const [pasteValue, setPasteValue] = useState('')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)

  const load = useCallback(() => {
    setLoading(true)
    getLicenses()
      .then(setLicenses)
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { load() }, [load])

  async function handleCreate() {
    setCreating(true)
    setCreateError(null)
    try {
      await createLicense(pasteValue.trim())
      toast(t('licenses.added'), 'success')
      setPasteValue('')
      setShowForm(false)
      load()
    } catch (e) {
      setCreateError((e as Error).message)
    } finally {
      setCreating(false)
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      await deleteLicense(deleteTarget)
      toast(t('licenses.deleted'), 'success')
      setDeleteTarget(null)
      load()
    } catch (e) {
      toast((e as Error).message, 'error')
    } finally {
      setDeleting(false)
    }
  }

  return (
    <div className="p-4 max-w-4xl">
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-xl font-semibold">{t('licenses.title')}</h1>
        <button
          type="button"
          className="inline-flex items-center gap-1 rounded border border-border px-2 py-1 text-sm hover:bg-muted"
          onClick={() => setShowForm(true)}
        >
          <Plus size={14} /> {t('licenses.add')}
        </button>
      </div>

      {error && <div className="rounded border border-destructive/40 bg-destructive/10 p-2 text-sm text-destructive mb-3">{error}</div>}
      {loading && <div className="text-sm text-muted-foreground">{t('common.loading')}</div>}

      {!loading && licenses.length === 0 && (
        <div className="text-sm text-muted-foreground">{t('licenses.empty')}</div>
      )}

      {!loading && licenses.length > 0 && (
        <table className="w-full text-sm">
          <thead className="text-xs uppercase text-muted-foreground">
            <tr>
              <th className="text-left py-2 pr-2">{t('licenses.col.tenant')}</th>
              <th className="text-left py-2 pr-2">{t('licenses.col.issuedTo')}</th>
              <th className="text-left py-2 pr-2">{t('licenses.col.validity')}</th>
              <th className="text-left py-2 pr-2">{t('licenses.col.status')}</th>
              <th className="text-right py-2">{t('licenses.col.actions')}</th>
            </tr>
          </thead>
          <tbody>
            {licenses.map((lic) => (
              <tr key={lic.id} className="border-t border-border/40">
                <td className="py-2 pr-2 font-mono">{lic.tenant}</td>
                <td className="py-2 pr-2">{lic.issuedTo || '—'}</td>
                <td className="py-2 pr-2 text-muted-foreground">
                  {lic.validFrom || '?'} — {lic.validUntil || '?'}
                </td>
                <td className="py-2 pr-2">
                  {lic.valid
                    ? <span className="inline-flex items-center gap-1 text-success"><BadgeCheck size={14} /> {t('licenses.status.valid')}</span>
                    : <span className="inline-flex items-center gap-1 text-muted-foreground"><BadgeX size={14} /> {t('licenses.status.invalid')}</span>}
                </td>
                <td className="py-2 text-right">
                  <button
                    type="button"
                    className="rounded p-1 text-muted-foreground hover:text-destructive hover:bg-muted"
                    title={t('licenses.delete')}
                    onClick={() => setDeleteTarget(lic.id)}
                  >
                    <Trash2 size={14} />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {showForm && (
        <div className="fixed inset-0 z-30 bg-black/40 flex items-center justify-center p-4">
          <div className="rounded border border-border bg-background p-4 w-full max-w-2xl">
            <h2 className="text-base font-semibold mb-2">{t('licenses.add')}</h2>
            <p className="text-xs text-muted-foreground mb-2">{t('licenses.hint')}</p>
            <textarea
              className="w-full h-64 rounded border border-border bg-muted/30 font-mono text-xs p-2"
              value={pasteValue}
              onChange={(e) => setPasteValue(e.target.value)}
              placeholder='{"id":"…","tenant":"…","signatures":[…]}'
            />
            {createError && <div className="mt-2 text-sm text-destructive">{createError}</div>}
            <div className="mt-3 flex justify-end gap-2">
              <button
                type="button"
                className="rounded border border-border px-3 py-1 text-sm hover:bg-muted"
                onClick={() => { setShowForm(false); setCreateError(null) }}
              >
                {t('common.cancel')}
              </button>
              <button
                type="button"
                className="rounded bg-primary px-3 py-1 text-sm text-primary-foreground disabled:opacity-50"
                disabled={creating || !pasteValue.trim()}
                onClick={handleCreate}
              >
                {creating ? t('licenses.adding') : t('licenses.add')}
              </button>
            </div>
          </div>
        </div>
      )}

      <ConfirmModal
        open={deleteTarget !== null}
        title={t('licenses.delete')}
        message={t('licenses.deleteConfirm', { id: deleteTarget ?? '' })}
        confirmLabel={t('common.delete')}
        confirmVariant="danger"
        loading={deleting}
        onConfirm={handleDelete}
        onCancel={() => setDeleteTarget(null)}
      />
    </div>
  )
}
