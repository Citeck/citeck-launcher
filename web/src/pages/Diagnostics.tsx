import { useEffect, useState, useCallback } from 'react'
import { getDiagnostics, postDiagnosticsFix } from '../lib/api'
import type { DiagnosticCheckDto } from '../lib/types'
import { useTranslation } from '../lib/i18n'
import { Stethoscope, RefreshCw, Wrench } from 'lucide-react'

const statusColor: Record<string, string> = {
  ok: 'text-green-500',
  warn: 'text-yellow-500',
  error: 'text-destructive',
}

const statusBg: Record<string, string> = {
  ok: 'bg-green-500/10',
  warn: 'bg-yellow-500/10',
  error: 'bg-destructive/10',
}

export function Diagnostics() {
  const { t } = useTranslation()
  const [checks, setChecks] = useState<DiagnosticCheckDto[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [fixing, setFixing] = useState(false)
  const [fixMessage, setFixMessage] = useState<string | null>(null)

  const runChecks = useCallback(async () => {
    setLoading(true)
    setError(null)
    setFixMessage(null)
    try {
      const result = await getDiagnostics()
      setChecks(result.checks)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { runChecks() }, [runChecks])

  async function handleFixAll() {
    setFixing(true)
    setFixMessage(null)
    try {
      const result = await postDiagnosticsFix()
      setFixMessage(t('diagnostics.fixResult', { fixed: result.fixed, failed: result.failed, message: result.message }))
      await runChecks()
    } catch (e) {
      setFixMessage((e as Error).message)
    } finally {
      setFixing(false)
    }
  }

  const hasFixable = checks.some((c) => c.fixable)

  return (
    <div className="p-3">
      <div className="flex items-center justify-between mb-2">
        <h1 className="text-base font-semibold flex items-center gap-1.5">
          <Stethoscope size={16} />
          {t('diagnostics.title')}
        </h1>
        <div className="flex items-center gap-2">
          <button
            type="button"
            className="flex items-center gap-1 rounded-md border border-border px-2.5 py-1 text-xs hover:bg-muted disabled:opacity-50"
            onClick={runChecks}
            disabled={loading}
          >
            <RefreshCw size={13} className={loading ? 'animate-spin' : ''} />
            {t('diagnostics.runChecks')}
          </button>
          <button
            type="button"
            className="flex items-center gap-1 rounded-md bg-primary text-primary-foreground px-2.5 py-1 text-xs font-medium hover:bg-primary/90 disabled:opacity-50"
            onClick={handleFixAll}
            disabled={!hasFixable || fixing}
          >
            <Wrench size={13} />
            {fixing ? t('diagnostics.fixing') : t('diagnostics.fixAll')}
          </button>
        </div>
      </div>

      {error && <div className="text-destructive text-xs mb-2">{error}</div>}
      {fixMessage && (
        <div className="text-xs mb-2 rounded border border-border bg-muted/30 px-2 py-1.5">{fixMessage}</div>
      )}

      <table className="w-full text-xs border-collapse">
        <thead>
          <tr className="text-left text-muted-foreground border-b border-border">
            <th className="py-1 pr-4 font-medium">{t('diagnostics.table.name')}</th>
            <th className="py-1 pr-4 font-medium w-20">{t('diagnostics.table.status')}</th>
            <th className="py-1 pr-4 font-medium">{t('diagnostics.table.message')}</th>
            <th className="py-1 font-medium w-16">{t('diagnostics.table.fixable')}</th>
          </tr>
        </thead>
        <tbody>
          {checks.map((c) => (
            <tr key={c.name} className="border-b border-border/20 hover:bg-muted/30">
              <td className="py-[3px] pr-4 font-mono">{c.name}</td>
              <td className="py-[3px] pr-4">
                <span className={`inline-block rounded px-1.5 py-0.5 text-[11px] font-medium ${statusColor[c.status] ?? 'text-muted-foreground'} ${statusBg[c.status] ?? ''}`}>
                  {c.status}
                </span>
              </td>
              <td className="py-[3px] pr-4 text-muted-foreground">{c.message}</td>
              <td className="py-[3px] text-muted-foreground">{c.fixable ? t('diagnostics.fixable.yes') : t('diagnostics.fixable.no')}</td>
            </tr>
          ))}
          {checks.length === 0 && !loading && (
            <tr><td colSpan={4} className="py-4 text-center text-muted-foreground">{t('diagnostics.empty')}</td></tr>
          )}
          {loading && checks.length === 0 && (
            <tr><td colSpan={4} className="py-4 text-center text-muted-foreground">{t('diagnostics.running')}</td></tr>
          )}
        </tbody>
      </table>
    </div>
  )
}
