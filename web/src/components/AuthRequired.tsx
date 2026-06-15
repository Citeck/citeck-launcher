import { useState } from 'react'
import { KeyRound } from 'lucide-react'
import { useAuthGateStore, submitAuthToken } from '../lib/authGate'
import { useTranslation } from '../lib/i18n'

/**
 * Full-screen gate shown when the daemon answers 401 AUTH_REQUIRED — i.e.
 * daemon.yml `api_auth` is enabled and the browser has neither a token nor a
 * session cookie. Two ways in: run `citeck ui` on the host (prints/opens an
 * authenticated /auth/session link), or paste the token here — the same
 * handshake runs via fetch and the page reloads with the session cookie set.
 */
export function AuthRequired() {
  const required = useAuthGateStore((s) => s.required)
  const { t } = useTranslation()
  const [token, setToken] = useState('')
  const [error, setError] = useState(false)
  const [busy, setBusy] = useState(false)

  if (!required) return null

  const submit = async () => {
    const trimmed = token.trim()
    if (!trimmed || busy) return
    setBusy(true)
    setError(false)
    const ok = await submitAuthToken(trimmed)
    if (ok) {
      window.location.reload()
      return
    }
    setError(true)
    setBusy(false)
  }

  return (
    <div className="fixed inset-0 z-[1000] bg-background flex items-center justify-center p-6">
      <div className="w-full max-w-md rounded-lg border border-border bg-card p-6 text-foreground shadow-xl">
        <div className="flex items-center gap-2 mb-2">
          <KeyRound size={18} className="text-primary" />
          <h2 className="text-lg font-semibold">{t('authGate.title')}</h2>
        </div>
        <p className="text-sm text-muted-foreground mb-4 whitespace-pre-line">
          {t('authGate.message', { cmd: 'citeck ui' })}
        </p>
        <input
          type="password"
          autoFocus
          value={token}
          onChange={(e) => { setToken(e.target.value); setError(false) }}
          onKeyDown={(e) => { if (e.key === 'Enter') void submit() }}
          placeholder={t('authGate.placeholder')}
          className="w-full px-3 py-2 bg-background border border-border rounded text-foreground focus:outline-none focus:border-primary"
        />
        {error && <p className="text-destructive text-sm mt-2">{t('authGate.error')}</p>}
        <div className="mt-4 flex justify-end">
          <button
            onClick={() => void submit()}
            disabled={!token.trim() || busy}
            className="rounded bg-primary text-primary-foreground px-4 py-1.5 text-sm font-medium disabled:opacity-50"
          >
            {t('authGate.submit')}
          </button>
        </div>
      </div>
    </div>
  )
}
