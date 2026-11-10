import { useEffect, useRef, useState } from 'react'
import { Check, Copy, Loader2, Plus, RefreshCw, ShieldAlert } from 'lucide-react'
import { ApiError, approveMCPOAuth, denyMCPOAuth, getMCPOAuthConnections, getMCPOAuthPending, revokeMCPOAuth, configureMCP, createMCPToken, getMCPStatus, revokeMCPToken } from '../lib/api'
import type { MCPOAuthConnection, MCPOAuthPending, MCPRole, MCPStatus, MCPToken } from '../lib/api'
import { copyText } from '../lib/clipboard'
import { formatDateTime } from '../lib/datetime'
import { useTranslation } from '../lib/i18n'
import { Modal, ModalField } from './Modal'

const controlClass = 'inline-flex items-center justify-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-sm focus-visible:outline-2 focus-visible:outline-primary disabled:opacity-50'
const buttonClass = `${controlClass} hover:bg-muted`
const primaryClass = `${controlClass} bg-primary text-background hover:brightness-95`
const inputClass = 'w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm focus:outline-none focus:border-primary'

// Exact wire values from launchermcp/oauth_transaction.go; unknown future
// warnings remain visible without guessing a connection's revocation outcome.
const storageUnconfirmed = 'MCP storage outcome is unconfirmed; MCP is closed until restart'
const revocationUnconfirmed = 'Revocation was NOT saved. This connection is denied in this process; retry is pending. If Launcher exits before a successful save, repeat the revocation after restart.'
const revocationErrorPrefix = 'MCP_OAUTH_REVOCATION_UNCONFIRMED'
type MCPWarningKey = 'mcp.oauth.storageWarning' | 'mcp.oauth.revokeWarning' | 'mcp.oauth.genericWarning'
function warningKey(warning?: string): MCPWarningKey | null {
  if (!warning) return null
  if (warning === storageUnconfirmed) return 'mcp.oauth.storageWarning'
  if (warning === revocationUnconfirmed) return 'mcp.oauth.revokeWarning'
  return 'mcp.oauth.genericWarning'
}

// Only confirmed newer connection reads retire known revocation debt. Storage
// closure and unknown status warnings require a new status observation.
function clearRevocationWarning(current: MCPStatus | null) {
  return current?.oauthWarning === revocationUnconfirmed ? { ...current, oauthWarning: undefined } : current
}

function CopyButton({ value, label }: { value: string; label: string }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])
  useEffect(() => {
    if (!copied) return
    const timer = setTimeout(() => setCopied(false), 2500)
    return () => clearTimeout(timer)
  }, [copied])
  return <button type="button" className={buttonClass} aria-label={label} onClick={(e) => {
    const dialog = e.currentTarget.closest('dialog')
    void (dialog ? copyText(value, dialog) : copyText(value)).then((success) => { if (mounted.current) setCopied(success) })
  }}>
    {copied ? <Check size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
    <span role="status">{copied ? t('mcp.copied') : t('mcp.copy')}</span>
  </button>
}

/** Daemon-global: deliberately independent of workspace and namespace stores. */
export function MCPSettings() {
  const { t, locale } = useTranslation()
  const [status, setStatus] = useState<MCPStatus | null>(null)
  const [enabled, setEnabled] = useState(false)
  const [port, setPort] = useState('19380')
  const [loadError, setLoadError] = useState('')
  const [loadAttempt, setLoadAttempt] = useState(0)
  const [configError, setConfigError] = useState('')
  const [saving, setSaving] = useState(false)
  const [revokeError, setRevokeError] = useState('')
  const [revoking, setRevoking] = useState<string | null>(null)
  const mutationPending = useRef(false)
  const mounted = useRef(true)
  const [refreshing, setRefreshing] = useState(false)
  const [refreshError, setRefreshError] = useState('')
  const [pending, setPending] = useState<MCPOAuthPending[]>([])
  const [connections, setConnections] = useState<MCPOAuthConnection[] | null>(null)
  const [oauthError, setOAuthError] = useState('')
  const [pendingError, setPendingError] = useState('')
  const [oauthWarning, setOAuthWarning] = useState('')
  const [oauthBusy, setOAuthBusy] = useState<string | null>(null)
  const oauthVersion = useRef(0)
  const approvedRequests = useRef(new Set<string>())
  const [creating, setCreating] = useState(false)
  const [now, setNow] = useState(Date.now)

  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])

  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30000)
    return () => clearInterval(timer)
  }, [])

  useEffect(() => {
    let active = true
    const controller = new AbortController()
    const version = oauthVersion.current
    getMCPOAuthConnections(controller.signal).then((value) => {
      if (active && version === oauthVersion.current) { setConnections(value.connections); setOAuthWarning(value.warning ?? '') }
    }).catch((err: unknown) => { if (active && version === oauthVersion.current) setOAuthError(String(err)) })
    getMCPStatus().then((value) => {
      if (!active || version !== oauthVersion.current) return
      setStatus(value); setEnabled(value.enabled); setPort(String(value.port))
    }).catch((err: unknown) => { if (active) setLoadError(String(err)) })
    return () => { active = false; controller.abort() }
  }, [loadAttempt])

  useEffect(() => {
    if (!status?.enabled) return
    let active = true
    let timer: ReturnType<typeof setTimeout> | undefined
    let controller: AbortController | undefined
    const poll = async () => {
      if (!active) return
      if (document.visibilityState === 'hidden' || mutationPending.current) {
        timer = setTimeout(() => void poll(), 3000); return
      }
      const version = oauthVersion.current
      controller = new AbortController()
      try {
        const value = await getMCPOAuthPending(controller.signal)
        if (active && version === oauthVersion.current) {
          setPending(value.pending); setPendingError('')
          for (const p of value.pending) if (p.status === 'approved') approvedRequests.current.add(p.id)
          const finished = [...approvedRequests.current].filter((id) => !value.pending.some((p) => p.id === id))
          if (finished.length) {
            // Completion creates the grant after owner approval. Retry failed
            // reads on the next poll, without polling connection Last used forever.
            const saved = await getMCPOAuthConnections(controller.signal)
            if (active && version === oauthVersion.current) {
              setConnections(saved.connections); setOAuthWarning(saved.warning ?? '')
              if (!saved.warning) setStatus(clearRevocationWarning)
              for (const id of finished) approvedRequests.current.delete(id)
            }
          }
        }
      } catch (err) { if (active && version === oauthVersion.current) setPendingError(String(err)) }
      if (active) timer = setTimeout(() => void poll(), 3000)
    }
    void poll()
    return () => { active = false; clearTimeout(timer); controller?.abort() }
  }, [status?.enabled])

  async function oauthAction(id: string, action: 'allow' | 'deny' | 'revoke', role: MCPRole = 'observer', approval?: { name: string; neverExpires: boolean; expiresAt?: string }) {
    if (mutationPending.current) return
    mutationPending.current = true
    oauthVersion.current++
    setOAuthBusy(id); setOAuthError('')
    try {
      if (action === 'allow') { await approveMCPOAuth(id, role, approval); approvedRequests.current.add(id) }
      else if (action === 'deny') await denyMCPOAuth(id)
      else await revokeMCPOAuth(id)
      if (!mounted.current) return
      // An accepted decision is never offered again, even if the refetch fails.
      if (action === 'revoke') setConnections((rows) => rows?.map((c) => c.id === id ? { ...c, revoked: true, revokedAt: new Date().toISOString() } : c) ?? null)
      else setPending((rows) => rows.filter((p) => p.id !== id))
      const results = await Promise.allSettled([
        getMCPOAuthPending().then((requests) => { if (mounted.current) setPending(requests.pending) }),
        getMCPOAuthConnections().then((saved) => {
          if (!mounted.current) return
          setConnections(saved.connections); setOAuthWarning(saved.warning ?? '')
          if (!saved.warning) setStatus(clearRevocationWarning)
        }),
      ])
      const failed = results.find((result) => result.status === 'rejected')
      if (failed?.status === 'rejected') throw failed.reason
    } catch (err) {
      if (mounted.current) {
        if (action === 'revoke' && err instanceof ApiError && (err.message === revocationErrorPrefix || err.message.startsWith(revocationErrorPrefix + ': '))) {
          // The daemon envelope carries the current storage warning, not always
          // retryable debt. Empty/unknown suffixes must still render generically.
          setOAuthWarning(err.message.slice(revocationErrorPrefix.length + 2) || revocationErrorPrefix)
        } else setOAuthError(String(err))
      }
    }
    finally { mutationPending.current = false; if (mounted.current) setOAuthBusy(null) }
  }

  async function refreshMetadata() {
    if (mutationPending.current) return
    mutationPending.current = true
    oauthVersion.current++
    setRefreshing(true); setRefreshError('')
    try {
      const results = await Promise.allSettled([
        getMCPStatus().then((value) => {
          // Apply independently of secondary reads; preserve unsaved inputs.
          if (mounted.current) setStatus(value)
        }),
        getMCPOAuthPending().then((value) => {
          if (mounted.current) { setPending(value.pending); setPendingError('') }
        }),
        getMCPOAuthConnections().then((value) => {
          if (mounted.current) {
            setConnections(value.connections); setOAuthWarning(value.warning ?? ''); setOAuthError('')
            if (!value.warning) setStatus(clearRevocationWarning)
          }
        }),
      ])
      const failed = results.find((result) => result.status === 'rejected')
      if (mounted.current && failed?.status === 'rejected') setRefreshError(String(failed.reason))
    } catch (err) { if (mounted.current) setRefreshError(String(err)) }
    finally { mutationPending.current = false; if (mounted.current) setRefreshing(false) }
  }

  function openCreate() {
    if (mutationPending.current) return
    // Hold through the native modal's lifetime, including its secondary GET.
    // A metadata response must never race token creation or hide its only value.
    mutationPending.current = true
    setCreating(true)
  }

  function closeCreate() {
    mutationPending.current = false
    setCreating(false)
  }

  async function save(e: React.FormEvent) {
    e.preventDefault()
    if (mutationPending.current) return
    mutationPending.current = true
    oauthVersion.current++
    setSaving(true); setConfigError('')
    try {
      const value = await configureMCP(enabled, Number(port))
      if (!mounted.current) return
      setStatus(value); setEnabled(value.enabled); setPort(String(value.port)); setPending([])
      // A port change permanently revokes OAuth grants. Reflect the saved
      // state even if the secondary metadata read fails; never repeat the PUT.
      try {
        const saved = await getMCPOAuthConnections()
        if (mounted.current) {
          setConnections(saved.connections); setOAuthWarning(saved.warning ?? ''); setRefreshError('')
          if (!saved.warning) setStatus(clearRevocationWarning)
        }
      } catch (err) { if (mounted.current) setRefreshError(String(err)) }
    } catch (err) { if (mounted.current) setConfigError(String(err)) } finally { mutationPending.current = false; if (mounted.current) setSaving(false) }
  }

  async function revoke(id: string) {
    if (mutationPending.current) return
    mutationPending.current = true
    setRevoking(id); setRevokeError('')
    try {
      const value = await revokeMCPToken(id)
      if (mounted.current) setStatus(value)
    } catch (err) { if (mounted.current) setRevokeError(String(err)) } finally { mutationPending.current = false; if (mounted.current) setRevoking(null) }
  }

  const warningKeys = [...new Set([warningKey(status?.oauthWarning), warningKey(oauthWarning)])].filter((key) => key !== null)
  const visibleWarningKeys = warningKeys.filter((key) => key !== 'mcp.oauth.revokeWarning' || !warningKeys.includes('mcp.oauth.storageWarning'))
  const unreliableEmptyList = warningKeys.includes('mcp.oauth.storageWarning') || warningKeys.includes('mcp.oauth.genericWarning')

  return (
    <section aria-label={t('mcp.title')} className="rounded-lg border border-border bg-card p-4 space-y-5">
      <div className="space-y-1">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h2 className="text-lg font-medium">{t('mcp.title')}</h2>
          {status && <span className={`text-xs font-medium ${status.error ? 'text-destructive' : 'text-muted-foreground'}`} role="status">
            {t(status.error ? 'mcp.failed' : status.listening ? 'mcp.listening' : 'mcp.notListening')}
          </span>}
        </div>
        <p className="text-xs text-muted-foreground">{t('mcp.description')}</p>
      </div>
      {visibleWarningKeys.map((key) => <p key={key} role="alert" className="text-sm text-destructive break-words">{t(key)}</p>)}
      {!status && (loadError ? <div className="space-y-2">
        <p role="alert" className="text-sm text-destructive break-words">{t('mcp.loadFailed')} {loadError}</p>
        <button type="button" className={buttonClass} onClick={() => { setLoadError(''); setLoadAttempt((n) => n + 1) }}>{t('mcp.retry')}</button>
      </div> : <div role="status" aria-label={t('mcp.loading')} className="space-y-3 motion-safe:animate-pulse">
        <div className="h-8 w-48 rounded bg-muted" /><div className="h-8 rounded bg-muted" /><div className="h-12 rounded bg-muted" />
      </div>)}
      {status && <>
        <form onSubmit={(e) => void save(e)} className="space-y-3">
          <div className="flex flex-wrap items-center gap-3">
            <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={enabled} disabled={saving} onChange={(e) => setEnabled(e.target.checked)} />{t('mcp.enabled')}</label>
            <button className={buttonClass} disabled={saving || !!revoking || refreshing || creating || !!oauthBusy} type="submit">{saving && <Loader2 size={14} className="animate-spin" aria-hidden="true" />}{t('mcp.save')}</button>
          </div>
          <details className="text-xs">
            <summary className="w-fit cursor-pointer text-muted-foreground hover:text-foreground">{t('mcp.advanced')}</summary>
            <label className="mt-3 flex flex-wrap items-center gap-3">{t('mcp.port')}<input className={`${inputClass} max-w-32`} type="number" required min={1024} max={65535} value={port} disabled={saving} onChange={(e) => setPort(e.target.value)} /></label>
          </details>
          {(configError || status.error) && <p role="alert" className="text-sm text-destructive break-words">{configError || status.error}</p>}
        </form>
        <div className="space-y-2">
          <h3 className="text-sm font-medium">{t('mcp.endpoint')}</h3>
          <div className="flex flex-wrap items-center gap-2">
            <code className="min-w-0 flex-1 basis-64 break-all text-sm select-all">{status.endpoint}</code>
            <CopyButton key={status.endpoint} value={status.endpoint} label={t('mcp.copyEndpoint')} />
          </div>
          {!status.listening && <p className="text-xs text-muted-foreground">{t('mcp.endpointInactive')}</p>}
          <p className="text-xs text-muted-foreground">{t('mcp.connectionTrust')}</p>
        </div>
        <div className="space-y-3 border-t border-border pt-4">
          <h3 className="text-sm font-medium">{t('mcp.oauth.pending')}</h3>
          <p className="text-xs text-muted-foreground">{t('mcp.oauth.signInHelp')}</p>
          {pendingError && <p role="alert" className="text-sm text-destructive break-words">{t('mcp.oauth.pendingFailed')} {pendingError}</p>}
          {pending.filter((p) => Date.parse(p.expiresAt) > now).length === 0 ? <p className="text-sm text-muted-foreground">{t('mcp.oauth.noPending')}</p> : <ul className="divide-y divide-border">
            {pending.filter((p) => Date.parse(p.expiresAt) > now).map((p) => <MCPPendingRequest key={p.id} request={p} busy={!!oauthBusy || saving || !!revoking || refreshing || creating} onDecision={(allow, role, approval) => void oauthAction(p.id, allow ? 'allow' : 'deny', role, approval)} />)}
          </ul>}
          <h3 className="pt-2 text-sm font-medium">{t('mcp.oauth.connections')}</h3>
          {connections === null ? <p className="text-sm text-muted-foreground">{t('mcp.loading')}</p> : connections.length === 0 ? <p className="text-sm text-muted-foreground">{t('mcp.oauth.noConnections')}</p> : <ul className="divide-y divide-border">
            {connections.map((c) => <li key={c.id} className="grid grid-cols-1 items-center gap-2 py-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] sm:gap-4 text-sm">
              <div className="min-w-0"><p className="font-medium break-words">{c.name || c.clientName || c.clientId}</p><p className="text-xs text-muted-foreground break-words">{c.clientName || c.clientId} ({t('mcp.oauth.unverified')}) · {t(`mcp.${c.role}`)}</p></div>
              <div className="space-y-1 text-xs text-muted-foreground">
                <p>{t('mcp.oauth.approved', { date: formatDateTime(c.approvedAt) || t('mcp.noData') })}</p>
                <p>{c.revoked ? t('mcp.revoked') : c.neverExpires ? t('mcp.neverExpires') : c.expiresAt ? Date.parse(c.expiresAt) <= now ? t('mcp.expired') : t('mcp.expires', { date: formatDateTime(c.expiresAt) || t('mcp.noData') }) : t('mcp.noData')}</p>
                <p>{t('mcp.lastUsed')}: {formatDateTime(c.lastUsedAt) || t('mcp.noData')}</p>
              </div>
              {!c.revoked && <button type="button" className={`${buttonClass} justify-self-start sm:justify-self-end`} disabled={!!oauthBusy || saving || !!revoking || refreshing || creating} aria-label={t('mcp.revokeNamed', { name: c.name || c.clientName || c.clientId })} onClick={() => void oauthAction(c.id, 'revoke')}>{oauthBusy === c.id && <Loader2 size={14} className="animate-spin" aria-hidden="true" />}{t('mcp.revoke')}</button>}
            </li>)}
          </ul>}
          {oauthError && <p role="alert" className="text-sm text-destructive break-words">{t('mcp.oauth.actionFailed')} {oauthError}</p>}
        </div>
        <div className="space-y-3 border-t border-border pt-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="text-sm font-medium">{t('mcp.tokens')}</h3>
            <div className="flex flex-wrap gap-2">
              <button type="button" className={buttonClass} aria-busy={refreshing} disabled={saving || !!revoking || refreshing || creating || !!oauthBusy} onClick={() => void refreshMetadata()}><RefreshCw size={14} className={refreshing ? 'animate-spin' : ''} aria-hidden="true" />{t('mcp.refreshMetadata')}</button>
              <button type="button" className={primaryClass} disabled={saving || !!revoking || refreshing || creating || !!oauthBusy} onClick={openCreate}><Plus size={14} aria-hidden="true" />{t('mcp.create')}</button>
            </div>
          </div>
          <p className="text-xs text-muted-foreground">{t('mcp.lastUsedHelp')}</p>
          {refreshError && <p role="alert" className="text-sm text-destructive break-words">{t('mcp.metadataRefreshFailed')} {refreshError}</p>}
          {status.tokens.length === 0 ? (unreliableEmptyList ? null : <p className="text-sm text-muted-foreground">{t('mcp.empty')}</p>) : <ul className="divide-y divide-border">
            {status.tokens.map((token) => {
              const lastUsed = formatDateTime(token.lastUsedAt)
              const expired = !!token.expiresAt && Date.parse(token.expiresAt) <= now
              return <li key={token.id} className="grid grid-cols-1 items-center gap-2 py-3 first:pt-0 last:pb-0 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] sm:gap-4 text-sm">
                <div className="min-w-0"><p className="font-medium break-words">{token.name}</p><p className="text-xs text-muted-foreground">{t(token.role === 'developer' ? 'mcp.developer' : 'mcp.observer')}</p></div>
                <div className="space-y-1 text-xs text-muted-foreground"><p>{token.revokedAt ? t('mcp.revoked') : expired ? t('mcp.expired') : token.expiresAt ? t('mcp.expires', { date: new Date(token.expiresAt).toLocaleDateString(locale) }) : t('mcp.neverExpires')}</p>
                  <p>{t('mcp.lastUsed')}: {lastUsed ? <time className="tabular-nums" dateTime={new Date(token.lastUsedAt!).toISOString()}>{lastUsed}</time> : t('mcp.noData')}</p>
                </div>
                {!token.revokedAt && <button type="button" className={`${buttonClass} justify-self-start sm:justify-self-end`} disabled={!!revoking || saving || refreshing || creating || !!oauthBusy} aria-label={t('mcp.revokeNamed', { name: token.name })} onClick={() => void revoke(token.id)}>{revoking === token.id && <Loader2 size={14} className="animate-spin" aria-hidden="true" />}{t('mcp.revoke')}</button>}
              </li>
            })}
          </ul>}
          {revokeError && <p role="alert" className="text-sm text-destructive break-words">{revokeError}</p>}
        </div>
      </>}
      {creating && <MCPTokenDialog onClose={closeCreate} onCreated={(token) => setStatus((current) => current ? { ...current, tokens: [...current.tokens, token] } : current)} onRefresh={(value) => setStatus(value)} />}
    </section>
  )
}

function MCPPendingRequest({ request, busy, onDecision }: { request: MCPOAuthPending; busy: boolean; onDecision: (allow: boolean, role: MCPRole, approval?: { name: string; neverExpires: boolean; expiresAt?: string }) => void }) {
  const { t } = useTranslation()
  const [role, setRole] = useState<MCPRole>('observer')
  const [name, setName] = useState(request.clientName || request.clientId)
  const [days, setDays] = useState('30')
  const [neverExpires, setNeverExpires] = useState(false)
  const validDays = Number.isInteger(Number(days)) && Number(days) >= 1 && Number(days) <= 36500
  return <li className="space-y-3 py-3">
    <div className="flex flex-wrap justify-between gap-2 text-sm">
      <p className="min-w-0 break-words font-medium">{request.clientName || request.clientId} <span className="text-xs font-normal text-muted-foreground">({t('mcp.oauth.unverified')})</span></p>
      <code className="select-all">{request.correlationCode}</code>
    </div>
    <p className="text-xs text-muted-foreground">{t('mcp.expires', { date: formatDateTime(request.expiresAt) || t('mcp.noData') })}</p>
    {request.status === 'approved' ? <p role="status" className="text-sm">{t('mcp.oauth.waitingBrowser')}</p> : <>
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="space-y-1 text-sm"><span>{t('mcp.oauth.connectionName')}</span><input className={inputClass} maxLength={100} disabled={busy} value={name} onChange={(e) => setName(e.target.value)} /></label>
        <label className="space-y-1 text-sm"><span>{t('mcp.days')}</span><input className={inputClass} type="number" min={1} max={36500} step={1} disabled={busy || neverExpires} value={days} onChange={(e) => setDays(e.target.value)} /></label>
      </div>
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" disabled={busy} checked={neverExpires} onChange={(e) => setNeverExpires(e.target.checked)} />{t('mcp.neverExpires')}</label>
      <fieldset disabled={busy} className="flex flex-wrap gap-4 text-sm">
        <legend className="mb-2 text-xs font-medium">{t('mcp.role')}</legend>
        {(['observer', 'developer'] as const).map((value) => <label key={value} className="flex items-center gap-2"><input type="radio" name={`mcp-oauth-role-${request.id}`} value={value} checked={role === value} onChange={() => setRole(value)} />{t(`mcp.${value}`)}</label>)}
      </fieldset>
      {role === 'developer' && <p role="note" className="flex items-start gap-2 text-sm"><ShieldAlert size={16} aria-hidden="true" className="mt-0.5 shrink-0 text-warning" /><span>{t('mcp.developerWarning')}</span></p>}
      <p className="text-xs text-muted-foreground">{t('mcp.scope')}</p>
      <div className="flex flex-wrap gap-2"><button type="button" className={primaryClass} disabled={busy || !name.trim() || (!neverExpires && !validDays)} onClick={() => onDecision(true, role, { name: name.trim(), neverExpires, ...(!neverExpires ? { expiresAt: new Date(Date.now() + Number(days) * 86400000).toISOString() } : {}) })}>{t('mcp.oauth.allow')}</button><button type="button" className={buttonClass} disabled={busy} onClick={() => onDecision(false, role)}>{t('mcp.oauth.deny')}</button></div>
    </>}
  </li>
}

function MCPTokenDialog({ onClose, onCreated, onRefresh }: { onClose: () => void; onCreated: (token: MCPToken) => void; onRefresh: (status: MCPStatus) => void }) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [role, setRole] = useState<MCPRole>('observer')
  const [neverExpires, setNeverExpires] = useState(false)
  const [days, setDays] = useState('30')
  const [oneTimeToken, setOneTimeToken] = useState('')
  const [error, setError] = useState('')
  const [refreshFailed, setRefreshFailed] = useState(false)
  const [busy, setBusy] = useState(false)
  const submitting = useRef(false)
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])
  const content = useRef<HTMLDivElement>(null)
  const nameInput = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (oneTimeToken) content.current?.querySelector('button')?.focus()
    else nameInput.current?.focus()
  }, [oneTimeToken])

  useEffect(() => {
    // Native Escape cancellation closes a dialog before onClose runs. Veto it
    // only for this dialog while the one-time result is still in flight.
    const preventPendingCancel = (event: Event) => {
      if (submitting.current && event.target instanceof HTMLDialogElement && content.current && event.target.contains(content.current)) event.preventDefault()
    }
    document.addEventListener('cancel', preventPendingCancel, true)
    return () => document.removeEventListener('cancel', preventPendingCancel, true)
  }, [])

  async function create(e: React.FormEvent) {
    e.preventDefault()
    if (submitting.current || oneTimeToken) return
    submitting.current = true
    setBusy(true); setError('')
    try {
      const result = await createMCPToken(name.trim(), role, neverExpires, neverExpires ? undefined : new Date(Date.now() + Number(days) * 86400000).toISOString())
      if (!mounted.current) return
      setOneTimeToken(result.value)
      onCreated(result.token)
      // Metadata is secondary: its failure must never discard the only raw value.
      try {
        const metadata = await getMCPStatus()
        if (mounted.current) onRefresh(metadata)
      } catch { if (mounted.current) setRefreshFailed(true) }
    } catch (err) { if (mounted.current) setError(String(err)) }
    finally { submitting.current = false; if (mounted.current) setBusy(false) }
  }

  return (
    <Modal
      open
      title={t(oneTimeToken ? 'mcp.created' : 'mcp.create')}
      onClose={() => { if (!submitting.current) onClose() }}
      onSubmit={oneTimeToken ? undefined : (e) => void create(e)}
      footer={
      oneTimeToken ? <button type="button" className={`${primaryClass} ml-auto`} disabled={busy} onClick={onClose}>{t('mcp.dismiss')}</button> : <>
        <button type="button" className={buttonClass} disabled={busy} onClick={onClose}>{t('mcp.cancel')}</button>
        <button type="submit" className={primaryClass} disabled={busy || !name.trim()}>{busy && <Loader2 size={14} className="animate-spin" aria-hidden="true" />}{t('mcp.create')}</button>
      </>
      }
    >
      <div ref={content} className="space-y-3">
        {oneTimeToken ? <>
          <p className="text-sm text-muted-foreground">{t('mcp.once')}</p>
          <code className="block rounded border border-border bg-background p-3 text-sm break-all select-all" data-testid="mcp-token-value">{oneTimeToken}</code>
          <CopyButton value={oneTimeToken} label={t('mcp.copyToken')} />
          {refreshFailed && <p role="status" className="text-xs text-muted-foreground">{t('mcp.refreshFailed')}</p>}
        </> : <>
          <ModalField label={t('mcp.name')} required><input ref={nameInput} aria-label={t('mcp.name')} className={inputClass} required maxLength={100} value={name} disabled={busy} onChange={(e) => setName(e.target.value)} /></ModalField>
          <fieldset disabled={busy} className="space-y-2">
            <legend className="mb-1 text-xs font-medium">{t('mcp.role')}</legend>
            {(['observer', 'developer'] as const).map((value) => <label key={value} className="flex items-start gap-2 text-sm cursor-pointer">
              <input type="radio" name="mcp-role" value={value} checked={role === value} className="mt-1" onChange={() => setRole(value)} />
              <span><span className="font-medium">{t(`mcp.${value}`)}</span><span className="block text-xs text-muted-foreground">{t(value === 'observer' ? 'mcp.observerDescription' : 'mcp.developerDescription')}</span></span>
            </label>)}
          </fieldset>
          <p className="text-xs text-muted-foreground">{t('mcp.scope')}</p>
          {role === 'developer' && <p role="note" className="flex items-start gap-2 text-sm"><ShieldAlert size={16} aria-hidden="true" className="mt-0.5 shrink-0 text-warning" /><span>{t('mcp.developerWarning')}</span></p>}
          <ModalField label={t('mcp.days')}><input aria-label={t('mcp.days')} className={inputClass} type="number" min={1} max={36500} step={1} required={!neverExpires} disabled={busy || neverExpires} value={days} onChange={(e) => setDays(e.target.value)} /></ModalField>
          <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={neverExpires} disabled={busy} onChange={(e) => setNeverExpires(e.target.checked)} />{t('mcp.neverExpires')}</label>
          <p className="text-xs text-muted-foreground">{t('mcp.createHint')}</p>
          {error && <p role="alert" className="text-sm text-destructive break-words">{error}</p>}
        </>}
        {busy && <p role="status" className="text-xs text-muted-foreground">{t('mcp.creating')}</p>}
      </div>
    </Modal>
  )
}
