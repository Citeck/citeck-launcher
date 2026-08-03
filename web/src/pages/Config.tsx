import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router'
import { FileCode2, KeyRound, Settings } from 'lucide-react'
import {
  getHealth,
  getMissingRegistryAuth,
  getRegistryBindings,
  getSecrets,
  listWorkspaces,
} from '../lib/api'
import type { HealthDto, SecretMetaDto, WorkspaceDto } from '../lib/types'
import { useTranslation } from '../lib/i18n'
import { useIsDesktop } from '../lib/daemonStatus'
import { WorkspaceFormDialog } from '../components/WorkspaceFormDialog'
import { WorkspaceConfigDialog } from '../components/WorkspaceConfigDialog'
import { RegistryCredentialsDialog } from '../components/RegistryCredentialsDialog'

/**
 * /config — the launcher's Settings page.
 *
 * Deliberately NOT namespace-scoped (see the route comment in App.tsx): it is
 * the one surface that stays reachable from Welcome AND from an open
 * namespace, so it hosts the two capabilities that were otherwise orphaned:
 *
 *   • **Workspace git settings** — previously only behind a hover-only pencil
 *     inside a dropdown that itself disappears once a namespace is open.
 *   • **Registry credentials** — previously only editable through
 *     error-triggered dialogs (pull-failure banner / pre-flight gate). Once a
 *     wrong secret was bound the daemon considered the host satisfied, so no
 *     error fired again and there was literally no way back. The read-only
 *     table below plus the rebind/unbind dialog is that way back.
 */
export function Config() {
  const { t } = useTranslation()
  const isDesktop = useIsDesktop()
  const [health, setHealth] = useState<HealthDto | null>(null)

  useEffect(() => {
    getHealth().then(setHealth).catch(() => {})
  }, [])

  return (
    <div className="p-4 max-w-4xl space-y-6">
      <div>
        <Link to="/" className="text-sm text-primary hover:underline">
          {t('config.back')}
        </Link>
        <h1 className="text-2xl font-semibold mt-2">{t('config.title')}</h1>
      </div>

      {/* Workspace git settings. Desktop-only: server mode is single-workspace
          by design and /workspaces 404s there (listWorkspaces resolves []). */}
      {isDesktop && <WorkspaceSection />}

      <RegistrySection />

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
    </div>
  )
}

/** Label/value row shared by the settings cards. */
function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex gap-3 text-sm">
      <span className="w-44 shrink-0 text-muted-foreground">{label}</span>
      <span className="min-w-0 break-all">{value}</span>
    </div>
  )
}

/**
 * Active-workspace git settings, read-only, with the TYPED form one click
 * away — the same WorkspaceFormDialog the Welcome-screen picker opens, so
 * there is exactly one place these fields are defined.
 */
function WorkspaceSection() {
  const { t } = useTranslation()
  const [workspaces, setWorkspaces] = useState<WorkspaceDto[]>([])
  const [editOpen, setEditOpen] = useState(false)
  const [rawOpen, setRawOpen] = useState(false)

  const reload = useCallback(async () => {
    try {
      setWorkspaces(await listWorkspaces())
    } catch {
      setWorkspaces([]) // daemon down / server mode — the card degrades to empty
    }
  }, [])

  useEffect(() => {
    // State is only set after an awaited fetch — not a cascading render.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void reload()
  }, [reload])

  const active = workspaces.find((w) => w.active)

  const authLabel = active?.authType === 'TOKEN'
    ? t('welcome.workspace.form.authType.token')
    : t('welcome.workspace.form.authType.none')

  return (
    <div className="rounded-lg border border-border bg-card p-4 space-y-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h2 className="text-lg font-medium">{t('config.workspace.title')}</h2>
          <p className="text-xs text-muted-foreground mt-0.5">{t('config.workspace.desc')}</p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <button
            type="button"
            disabled={!active}
            className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50"
            onClick={() => setEditOpen(true)}
          >
            <Settings size={12} />
            {t('config.workspace.settings')}
          </button>
          {/* Power-user escape hatch, kept visually secondary — the raw YAML
              editor is not what someone looking for "git settings" wants. */}
          <button
            type="button"
            disabled={!active}
            className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
            onClick={() => setRawOpen(true)}
          >
            <FileCode2 size={12} />
            {t('workspace.config.rawEdit')}
          </button>
        </div>
      </div>

      {active ? (
        <div className="space-y-1.5">
          <Row label={t('welcome.workspace.form.name')} value={active.name || active.id} />
          <Row label={t('welcome.workspace.form.repoUrl')} value={active.repoUrl || '—'} />
          <Row label={t('welcome.workspace.form.repoBranch')} value={active.repoBranch || '—'} />
          <Row label={t('welcome.workspace.form.repoPullPeriod')} value={active.repoPullPeriod || '—'} />
          <Row label={t('welcome.workspace.form.authType')} value={authLabel} />
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">{t('config.workspace.none')}</p>
      )}

      {editOpen && active && (
        <WorkspaceFormDialog
          mode={{ kind: 'edit', ws: active }}
          onClose={() => setEditOpen(false)}
          onSaved={() => { setEditOpen(false); void reload() }}
        />
      )}
      {rawOpen && active && (
        <WorkspaceConfigDialog
          workspaceId={active.id}
          onClose={() => setRawOpen(false)}
          onSaved={() => void reload()}
        />
      )}
    </div>
  )
}

/**
 * Registry host → secret bindings.
 *
 * Rows are the union of what the daemon has BOUND (`GET /registry-bindings`)
 * and the auth-required hosts it still considers UNRESOLVED
 * (`GET /registry-bindings/missing`) — the second set is what the pre-flight
 * gate prompts for, surfaced here so it can be handled before a pull fails.
 */
function RegistrySection() {
  const { t } = useTranslation()
  const [bindings, setBindings] = useState<Record<string, string>>({})
  const [missing, setMissing] = useState<string[]>([])
  const [secrets, setSecrets] = useState<SecretMetaDto[]>([])
  // Host whose credentials dialog is open (null = closed). Keyed by host so the
  // dialog remounts per row and re-reads the persisted binding.
  const [target, setTarget] = useState<string | null>(null)

  const reload = useCallback(async () => {
    const [b, m, s] = await Promise.all([
      getRegistryBindings().catch(() => ({} as Record<string, string>)),
      getMissingRegistryAuth().catch(() => [] as string[]),
      // Names only — secret VALUES are write-only and never leave the daemon.
      getSecrets().catch(() => [] as SecretMetaDto[]),
    ])
    setBindings(b)
    setMissing(m)
    setSecrets(s.filter((x) => x.type === 'REGISTRY_AUTH'))
  }, [])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void reload()
  }, [reload])

  const hosts = Array.from(new Set([...Object.keys(bindings), ...missing])).sort()

  function secretLabel(host: string): React.ReactNode {
    const id = bindings[host]
    if (!id) return <span className="text-warning">{t('config.registry.unbound')}</span>
    const match = secrets.find((s) => s.id === id)
    if (!match) {
      // Bound to a secret that no longer exists — say so instead of printing a
      // bare id that looks like a healthy value.
      return <span className="text-destructive">{t('config.registry.unknownSecret', { id })}</span>
    }
    return match.name || match.id
  }

  return (
    <div className="rounded-lg border border-border bg-card p-4 space-y-3">
      <div>
        <h2 className="text-lg font-medium flex items-center gap-1.5">
          <KeyRound size={16} />
          {t('config.registry.title')}
        </h2>
        <p className="text-xs text-muted-foreground mt-0.5">{t('config.registry.desc')}</p>
      </div>

      {hosts.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('config.registry.empty')}</p>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-xs text-muted-foreground">
              <th className="pb-1 font-medium">{t('config.registry.colHost')}</th>
              <th className="pb-1 font-medium">{t('config.registry.colSecret')}</th>
              <th className="pb-1" />
            </tr>
          </thead>
          <tbody>
            {hosts.map((host) => (
              <tr key={host} className="border-t border-border">
                <td className="py-1.5 pr-3 break-all">{host}</td>
                <td className="py-1.5 pr-3 break-all">{secretLabel(host)}</td>
                <td className="py-1.5 text-right">
                  {/* One action, one dialog: RegistryCredentialsDialog already
                      does pick-existing / create-new / unbind. Duplicating any
                      of that here would fork the binding logic. */}
                  <button
                    type="button"
                    className="rounded-md border border-border px-2.5 py-1 text-xs hover:bg-muted"
                    onClick={() => setTarget(host)}
                  >
                    {t('config.registry.configure')}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {target && (
        <RegistryCredentialsDialog
          open
          manage
          host={target}
          onSaved={() => { setTarget(null); void reload() }}
          onClose={() => setTarget(null)}
        />
      )}
    </div>
  )
}
