import { useState } from 'react'
import { useNavigate } from 'react-router'
import { createNamespace } from '../lib/api'
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
  { label: 'Name', key: 'name' },
  { label: 'Authentication', key: 'auth' },
  { label: 'Users', key: 'users' },
  { label: 'Hostname', key: 'host' },
  { label: 'TLS', key: 'tls' },
  { label: 'Port', key: 'port' },
  { label: 'PgAdmin', key: 'pgadmin' },
  { label: 'Review', key: 'review' },
] as const

function defaultPort(tls: string): number {
  return tls !== 'none' ? 443 : 80
}

export function Wizard() {
  const navigate = useNavigate()
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
    { label: 'Name', value: state.name },
    { label: 'Authentication', value: state.authType },
    ...(state.authType === 'BASIC' && state.users ? [{ label: 'Users', value: state.users }] : []),
    { label: 'Hostname', value: state.host || 'localhost' },
    { label: 'TLS', value: state.tls },
    { label: 'Port', value: String(state.port) },
    { label: 'PgAdmin', value: state.pgAdmin ? 'Enabled' : 'Disabled' },
  ]

  return (
    <div className="p-3 max-w-xl mx-auto">
      <h1 className="text-base font-semibold flex items-center gap-1.5 mb-4">
        <Wand2 size={16} />
        Create Namespace
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
              {s.label}
            </span>
          </div>
        ))}
      </div>

      {/* Step content */}
      <div className="rounded border border-border bg-card p-4 mb-4 min-h-[120px]">
        {currentStep.key === 'name' && (
          <div>
            <label className="block text-sm font-medium mb-1">Namespace Name</label>
            <p className="text-xs text-muted-foreground mb-2">Choose a unique name for your namespace.</p>
            <input
              type="text"
              className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary"
              value={state.name}
              onChange={(e) => update('name', e.target.value)}
              placeholder="my-namespace"
              autoFocus
            />
          </div>
        )}

        {currentStep.key === 'auth' && (
          <div>
            <label className="block text-sm font-medium mb-1">Authentication Type</label>
            <p className="text-xs text-muted-foreground mb-2">Select how users will authenticate.</p>
            <select
              className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary"
              value={state.authType}
              onChange={(e) => update('authType', e.target.value as 'BASIC' | 'KEYCLOAK')}
            >
              <option value="BASIC">Basic Authentication</option>
              <option value="KEYCLOAK">Keycloak SSO</option>
            </select>
          </div>
        )}

        {currentStep.key === 'users' && (
          <div>
            <label className="block text-sm font-medium mb-1">Users</label>
            <p className="text-xs text-muted-foreground mb-2">Comma-separated list of usernames to create.</p>
            <input
              type="text"
              className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary"
              value={state.users}
              onChange={(e) => update('users', e.target.value)}
              placeholder="admin, user1, user2"
            />
          </div>
        )}

        {currentStep.key === 'host' && (
          <div>
            <label className="block text-sm font-medium mb-1">Hostname</label>
            <p className="text-xs text-muted-foreground mb-2">The hostname for the namespace.</p>
            <input
              type="text"
              className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary"
              value={state.host}
              onChange={(e) => update('host', e.target.value)}
              placeholder="localhost"
            />
          </div>
        )}

        {currentStep.key === 'tls' && (
          <div>
            <label className="block text-sm font-medium mb-1">TLS Configuration</label>
            <p className="text-xs text-muted-foreground mb-2">Choose the TLS/HTTPS mode.</p>
            <select
              className="w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary"
              value={state.tls}
              onChange={(e) => update('tls', e.target.value as WizardState['tls'])}
            >
              <option value="none">None (HTTP only)</option>
              <option value="self-signed">Self-Signed Certificate</option>
              <option value="letsencrypt">Let&apos;s Encrypt</option>
            </select>
          </div>
        )}

        {currentStep.key === 'port' && (
          <div>
            <label className="block text-sm font-medium mb-1">Port</label>
            <p className="text-xs text-muted-foreground mb-2">
              The port number for the namespace (default: {defaultPort(state.tls)}).
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
            <label className="block text-sm font-medium mb-1">PgAdmin</label>
            <p className="text-xs text-muted-foreground mb-2">Enable PgAdmin for database management.</p>
            <label className="flex items-center gap-2 text-sm cursor-pointer">
              <input
                type="checkbox"
                className="rounded border-border"
                checked={state.pgAdmin}
                onChange={(e) => update('pgAdmin', e.target.checked)}
              />
              Enable PgAdmin
            </label>
          </div>
        )}

        {currentStep.key === 'review' && (
          <div>
            <label className="block text-sm font-medium mb-2">Review Configuration</label>
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
        <button
          type="button"
          className="flex items-center gap-1 rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50 disabled:pointer-events-none"
          onClick={handleBack}
          disabled={isFirst}
        >
          <ChevronLeft size={14} />
          Back
        </button>
        {isLast ? (
          <button
            type="button"
            className="flex items-center gap-1 rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-xs font-medium hover:bg-primary/90 disabled:opacity-50"
            onClick={handleCreate}
            disabled={creating}
          >
            {creating ? 'Creating...' : 'Create'}
            <Check size={14} />
          </button>
        ) : (
          <button
            type="button"
            className="flex items-center gap-1 rounded-md bg-primary text-primary-foreground px-3 py-1.5 text-xs font-medium hover:bg-primary/90"
            onClick={handleNext}
          >
            Next
            <ChevronRight size={14} />
          </button>
        )}
      </div>
    </div>
  )
}
