import { useState } from 'react'
import { useNavigate } from 'react-router'
import { createNamespace } from '../lib/api'
import { toast } from '../lib/toast'
import { useTranslation } from '../lib/i18n'
import { Wand2, ChevronLeft, ChevronRight, Check } from 'lucide-react'

interface WizardState {
  name: string
  authType: 'BASIC' | 'KEYCLOAK'
  users: string
  host: string
  tls: 'none' | 'self-signed' | 'letsencrypt'
  port: number
  pgAdmin: boolean
}

const STEPS = [
  { labelKey: 'wizard.step.name', key: 'name' },
  { labelKey: 'wizard.step.auth', key: 'auth' },
  { labelKey: 'wizard.step.users', key: 'users' },
  { labelKey: 'wizard.step.host', key: 'host' },
  { labelKey: 'wizard.step.tls', key: 'tls' },
  { labelKey: 'wizard.step.port', key: 'port' },
  { labelKey: 'wizard.step.pgadmin', key: 'pgadmin' },
  { labelKey: 'wizard.step.review', key: 'review' },
] as const

function defaultPort(tls: string): number {
  return tls !== 'none' ? 443 : 80
}

export function Wizard() {
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [step, setStep] = useState(0)
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [state, setState] = useState<WizardState>({
    name: '',
    authType: 'BASIC',
    users: '',
    host: 'localhost',
    tls: 'none',
    port: 80,
    pgAdmin: false,
  })

  // Skip users step if auth is KEYCLOAK
  const visibleSteps = STEPS.filter((s) => {
    if (s.key === 'users' && state.authType !== 'BASIC') return false
    return true
  })

  const currentStep = visibleSteps[step]
  const isFirst = step === 0
  const isLast = step === visibleSteps.length - 1

  function handleNext() {
    if (currentStep.key === 'name' && !state.name.trim()) return
    if (!isLast) setStep(step + 1)
  }

  function handleBack() {
    if (!isFirst) setStep(step - 1)
  }

  async function handleCreate() {
    setCreating(true)
    setError(null)
    try {
      const users = state.users
        .split(',')
        .map((u) => u.trim())
        .filter(Boolean)
      await createNamespace({
        name: state.name.trim(),
        authType: state.authType,
        users: users.length > 0 ? users : undefined,
        host: state.host || 'localhost',
        port: state.port,
        tlsEnabled: state.tls !== 'none',
        tlsMode: state.tls !== 'none' ? state.tls : undefined,
        pgAdminEnabled: state.pgAdmin,
        bundleRepo: '',
        bundleKey: '',
      })
      toast(t('wizard.success'), 'success')
      navigate('/')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setCreating(false)
    }
  }

  function update<K extends keyof WizardState>(key: K, value: WizardState[K]) {
    setState((prev) => {
      const next = { ...prev, [key]: value }
      // Auto-update port when TLS changes
      if (key === 'tls') {
        next.port = defaultPort(value as string)
      }
      return next
    })
  }

  const reviewItems: { label: string; value: string }[] = [
    { label: t('wizard.step.name'), value: state.name },
    { label: t('wizard.step.auth'), value: state.authType },
    ...(state.authType === 'BASIC' && state.users ? [{ label: t('wizard.step.users'), value: state.users }] : []),
    { label: t('wizard.step.host'), value: state.host || 'localhost' },
    { label: t('wizard.step.tls'), value: state.tls },
    { label: t('wizard.step.port'), value: String(state.port) },
    { label: t('wizard.step.pgadmin'), value: state.pgAdmin ? 'Enabled' : 'Disabled' },
  ]

  return (
    <div className="p-3 max-w-xl mx-auto">
      <h1 className="text-base font-semibold flex items-center gap-1.5 mb-4">
        <Wand2 size={16} />
        {t('wizard.title')}
      </h1>

      {/* Step indicators */}
      <div className="flex items-center gap-1 mb-6">
        {visibleSteps.map((s, i) => (
          <div key={s.key} className="flex items-center gap-1">
            {i > 0 && <div className="w-4 h-px bg-border" />}
            <div
              className={`flex items-center justify-center w-6 h-6 rounded-full text-[11px] font-medium border ${
                i === step
                  ? 'border-primary bg-primary text-primary-foreground'
                  : i < step
                    ? 'border-primary/40 bg-primary/10 text-primary'
                    : 'border-border text-muted-foreground'
              }`}
            >
              {i < step ? <Check size={12} /> : i + 1}
            </div>
            <span className={`text-xs ${i === step ? 'text-foreground font-medium' : 'text-muted-foreground'}`}>
              {t(s.labelKey)}
            </span>
          </div>
        ))}
      </div>

      {/* Step content */}
      <div className="rounded border border-border bg-card p-4 mb-4 min-h-[120px]">
        {currentStep.key === 'name' && (
          <div>
            <label className="block text-sm font-medium mb-1">{t('wizard.name.label')}</label>
            <p className="text-xs text-muted-foreground mb-2">{t('wizard.name.hint')}</p>
            <input
              type="text"
              className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary"
              value={state.name}
              onChange={(e) => update('name', e.target.value)}
              placeholder={t('wizard.name.placeholder')}
              autoFocus
            />
          </div>
        )}

        {currentStep.key === 'auth' && (
          <div>
            <label className="block text-sm font-medium mb-1">{t('wizard.auth.label')}</label>
            <p className="text-xs text-muted-foreground mb-2">{t('wizard.auth.hint')}</p>
            <select
              className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary"
              value={state.authType}
              onChange={(e) => update('authType', e.target.value as 'BASIC' | 'KEYCLOAK')}
            >
              <option value="BASIC">{t('wizard.auth.basic')}</option>
              <option value="KEYCLOAK">{t('wizard.auth.keycloak')}</option>
            </select>
          </div>
        )}

        {currentStep.key === 'users' && (
          <div>
            <label className="block text-sm font-medium mb-1">{t('wizard.users.label')}</label>
            <p className="text-xs text-muted-foreground mb-2">{t('wizard.users.hint')}</p>
            <input
              type="text"
              className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary"
              value={state.users}
              onChange={(e) => update('users', e.target.value)}
              placeholder={t('wizard.users.placeholder')}
            />
          </div>
        )}

        {currentStep.key === 'host' && (
          <div>
            <label className="block text-sm font-medium mb-1">{t('wizard.host.label')}</label>
            <p className="text-xs text-muted-foreground mb-2">{t('wizard.host.hint')}</p>
            <input
              type="text"
              className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary"
              value={state.host}
              onChange={(e) => update('host', e.target.value)}
              placeholder={t('wizard.host.placeholder')}
            />
          </div>
        )}

        {currentStep.key === 'tls' && (
          <div>
            <label className="block text-sm font-medium mb-1">{t('wizard.tls.label')}</label>
            <p className="text-xs text-muted-foreground mb-2">{t('wizard.tls.hint')}</p>
            <select
              className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary"
              value={state.tls}
              onChange={(e) => update('tls', e.target.value as WizardState['tls'])}
            >
              <option value="none">{t('wizard.tls.none')}</option>
              <option value="self-signed">{t('wizard.tls.selfSigned')}</option>
              <option value="letsencrypt">{t('wizard.tls.letsencrypt')}</option>
            </select>
          </div>
        )}

        {currentStep.key === 'port' && (
          <div>
            <label className="block text-sm font-medium mb-1">{t('wizard.port.label')}</label>
            <p className="text-xs text-muted-foreground mb-2">
              {t('wizard.port.hint')}
            </p>
            <input
              type="number"
              className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary"
              value={state.port}
              onChange={(e) => update('port', parseInt(e.target.value, 10) || 0)}
              min={1}
              max={65535}
            />
          </div>
        )}

        {currentStep.key === 'pgadmin' && (
          <div>
            <label className="block text-sm font-medium mb-1">{t('wizard.pgadmin.label')}</label>
            <p className="text-xs text-muted-foreground mb-2">{t('wizard.pgadmin.hint')}</p>
            <label className="flex items-center gap-2 text-sm cursor-pointer">
              <input
                type="checkbox"
                className="rounded border-border"
                checked={state.pgAdmin}
                onChange={(e) => update('pgAdmin', e.target.checked)}
              />
              {t('wizard.pgadmin.enable')}
            </label>
          </div>
        )}

        {currentStep.key === 'review' && (
          <div>
            <label className="block text-sm font-medium mb-2">{t('wizard.review.label')}</label>
            <div className="space-y-1">
              {reviewItems.map((item) => (
                <div key={item.label} className="flex justify-between text-xs py-0.5 border-b border-border/20">
                  <span className="text-muted-foreground">{item.label}</span>
                  <span className="font-mono">{item.value}</span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>

      {error && <div className="text-destructive text-xs mb-2">{error}</div>}

      {/* Navigation */}
      <div className="flex justify-between">
        <div className="flex items-center gap-2">
          <button
            type="button"
            className="flex items-center gap-1 rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50 disabled:pointer-events-none"
            onClick={handleBack}
            disabled={isFirst}
          >
            <ChevronLeft size={14} />
            {t('wizard.back')}
          </button>
          <button
            type="button"
            className="rounded-md border border-border px-3 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
            onClick={() => navigate('/')}
          >
            {t('wizard.cancel')}
          </button>
        </div>
        {isLast ? (
          <button
            type="button"
            className="flex items-center gap-1 rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-xs font-medium hover:bg-primary/90 disabled:opacity-50"
            onClick={handleCreate}
            disabled={creating}
          >
            {creating ? t('wizard.creating') : t('wizard.create')}
            <Check size={14} />
          </button>
        ) : (
          <button
            type="button"
            className="flex items-center gap-1 rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-xs font-medium hover:bg-primary/90"
            onClick={handleNext}
          >
            {t('wizard.next')}
            <ChevronRight size={14} />
          </button>
        )}
      </div>
    </div>
  )
}
