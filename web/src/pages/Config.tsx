import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { getHealth } from '../lib/api'
import type { HealthDto } from '../lib/types'
import { ConfigEditor } from '../components/ConfigEditor'
import { useTranslation } from '../lib/i18n'

export function Config() {
  const { t } = useTranslation()
  const [health, setHealth] = useState<HealthDto | null>(null)

  useEffect(() => {
    getHealth().then(setHealth).catch(() => {})
  }, [])

  return (
    <div className="space-y-6">
      <div>
        <Link to="/" className="text-sm text-primary hover:underline">
          {t('config.back')}
        </Link>
        <h1 className="text-2xl font-semibold mt-2">{t('config.title')}</h1>
      </div>

      {/* Health checks */}
      {health && (
        <div className="rounded-lg border border-border bg-card p-4 space-y-3">
          <h2 className="text-lg font-medium">{t('config.health.title')}</h2>
          <div
            className={`rounded-md px-3 py-2 text-sm ${
              health.healthy
                ? 'bg-success/10 text-success border border-success/20'
                : 'bg-destructive/10 text-destructive border border-destructive/20'
            }`}
          >
            {health.healthy ? t('config.health.ok') : t('config.health.issues')}
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

      <ConfigEditor />
    </div>
  )
}
