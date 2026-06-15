import { Check, X, Loader2, CheckCircle2 } from 'lucide-react'
import { useDashboardStore } from '../lib/store'
import { useQuickStartStore } from '../lib/quickstart'
import { useTranslation, type LocaleKey } from '../lib/i18n'
import { deriveStartProgress, type StageId, type StageInfo } from '../lib/startProgress'

// Theme-aware status colors (same CSS tokens StatusBadge uses) so the stepper
// reads correctly on both dark (Darcula/Lens) and light surfaces.
const C_DONE = 'var(--color-status-running)'
const C_ACTIVE = 'var(--color-status-transient)'
const C_ERROR = 'var(--color-status-stalled)'

const STAGE_LABEL: Record<StageId, LocaleKey> = {
  bundle: 'startProgress.stage.bundle',
  images: 'startProgress.stage.images',
  infra: 'startProgress.stage.infra',
  apps: 'startProgress.stage.apps',
  ready: 'startProgress.stage.ready',
}

interface StartProgressStepperProps {
  /** Navigate to the Dashboard (and dismiss the stepper). */
  onOpenDashboard: () => void
}

/**
 * Compact bootstrap progress stepper shown on Welcome during a quick start.
 * Fed entirely by the existing SSE-driven dashboard store (namespace + app
 * statuses + live pull progress) — no extra polling. The user can navigate
 * away at any time; the quick-start store keeps the stepper alive across
 * remounts.
 */
export function StartProgressStepper({ onOpenDashboard }: StartProgressStepperProps) {
  const { t, tDynamic } = useTranslation()
  const namespace = useDashboardStore((s) => s.namespace)
  const pullProgress = useDashboardStore((s) => s.pullProgress)
  const creating = useQuickStartStore((s) => s.creating)
  const createError = useQuickStartStore((s) => s.error)
  const dismiss = useQuickStartStore((s) => s.dismiss)

  const model = deriveStartProgress({
    creating,
    nsStatus: namespace?.status ?? null,
    apps: namespace?.apps ?? [],
    pullProgress,
  })

  // A fatal create/start request error pins the bundle stage to the error
  // state — there is no namespace to derive failures from. The error dialog
  // itself is owned by Welcome (showError), unchanged.
  const stages = createError
    ? model.stages.map((s) => (s.id === 'bundle' ? { ...s, state: 'error' as const } : s))
    : model.stages
  const failed = model.failed || !!createError

  return (
    <div className="w-full rounded-lg border border-border bg-card p-5 shadow-sm">
      {/* Header */}
      <div className="flex items-center gap-2.5 mb-4">
        {model.running ? (
          <CheckCircle2 size={18} style={{ color: C_DONE }} />
        ) : failed ? (
          <X size={18} style={{ color: C_ERROR }} />
        ) : (
          <Loader2 size={18} className="animate-spin" style={{ color: C_ACTIVE }} />
        )}
        <h2 className="text-sm font-semibold text-foreground">
          {model.running
            ? t('startProgress.success')
            : failed
              ? t('startProgress.failed')
              : t('startProgress.title')}
        </h2>
      </div>

      {/* Stages */}
      <ol className="flex flex-col">
        {stages.map((stage, i) => (
          <li key={stage.id} className="flex gap-3">
            {/* Marker + connector */}
            <div className="flex flex-col items-center">
              <StageMarker state={stage.state} />
              {i < stages.length - 1 && (
                <div
                  className="w-px flex-1 min-h-3 my-0.5"
                  style={{
                    backgroundColor: stage.state === 'done' ? C_DONE : 'var(--color-border)',
                    opacity: stage.state === 'done' ? 0.5 : 0.8,
                  }}
                />
              )}
            </div>
            {/* Label + detail */}
            <div className="pb-3 min-w-0">
              <div
                className={`text-sm leading-5 ${
                  stage.state === 'pending' ? 'text-muted-foreground' : 'text-foreground'
                } ${stage.state === 'active' ? 'font-medium' : ''}`}
              >
                {t(STAGE_LABEL[stage.id])}
              </div>
              <StageDetail stage={stage} createError={stage.id === 'bundle' ? createError : null} t={t} tDynamic={tDynamic} />
            </div>
          </li>
        ))}
      </ol>

      {/* Actions — never blocking: the user can leave for the Dashboard at any
          point; on error, Hide returns to the Welcome buttons (retry path). */}
      <div className="mt-2 flex justify-end gap-3">
        {(failed || model.running) && (
          <button
            type="button"
            onClick={dismiss}
            className="rounded-md border border-border px-4 py-2 text-sm hover:bg-muted text-foreground"
          >
            {t('startProgress.hide')}
          </button>
        )}
        <button
          type="button"
          disabled={!namespace}
          onClick={onOpenDashboard}
          className="rounded-md bg-muted hover:bg-muted/70 px-4 py-2 text-sm font-medium text-foreground transition-colors disabled:opacity-50"
        >
          {t('startProgress.openDashboard')}
        </button>
      </div>
    </div>
  )
}

function StageMarker({ state }: { state: StageInfo['state'] }) {
  const base = 'flex h-5 w-5 items-center justify-center rounded-full shrink-0'
  switch (state) {
    case 'done':
      return (
        <span className={base} style={{ backgroundColor: `color-mix(in srgb, ${C_DONE} 18%, transparent)` }}>
          <Check size={12} style={{ color: C_DONE }} strokeWidth={3} />
        </span>
      )
    case 'active':
      return (
        <span className={base} style={{ backgroundColor: `color-mix(in srgb, ${C_ACTIVE} 18%, transparent)` }}>
          <Loader2 size={12} className="animate-spin" style={{ color: C_ACTIVE }} />
        </span>
      )
    case 'error':
      return (
        <span className={base} style={{ backgroundColor: `color-mix(in srgb, ${C_ERROR} 18%, transparent)` }}>
          <X size={12} style={{ color: C_ERROR }} strokeWidth={3} />
        </span>
      )
    default:
      return (
        <span className={base}>
          <span className="h-1.5 w-1.5 rounded-full bg-muted-foreground/40" />
        </span>
      )
  }
}

interface StageDetailProps {
  stage: StageInfo
  /** Fatal create-request error text (bundle stage only). */
  createError: string | null
  t: ReturnType<typeof useTranslation>['t']
  tDynamic: ReturnType<typeof useTranslation>['tDynamic']
}

function StageDetail({ stage, createError, t, tDynamic }: StageDetailProps) {
  if (createError) {
    return (
      <div className="text-xs mt-0.5 break-words" style={{ color: C_ERROR }}>
        {createError}
      </div>
    )
  }
  if (stage.state !== 'active' && stage.state !== 'error') return null

  const lines: React.ReactNode[] = []
  if (stage.id === 'images' && stage.total !== undefined) {
    lines.push(
      <span key="count">{t('startProgress.detail.images', { pulled: stage.done ?? 0, total: stage.total })}</span>,
    )
    const top = stage.pulls?.[0]
    if (top) {
      const extra = (stage.pulls?.length ?? 0) - 1
      lines.push(
        <span key="pull">
          {top.name}: {top.percent}%{top.phase ? ` · ${top.phase}` : ''}
          {extra > 0 ? ` (+${extra})` : ''}
        </span>,
      )
    }
  }
  if ((stage.id === 'infra' || stage.id === 'apps') && stage.total !== undefined) {
    lines.push(
      <span key="count">{t('startProgress.detail.apps', { running: stage.done ?? 0, total: stage.total })}</span>,
    )
    if (stage.currentApp && stage.state !== 'error') {
      // Status display name reuses the existing dynamic 'status.*' keys.
      lines.push(
        <span key="cur">
          {stage.currentApp.name} · {tDynamic('status.' + stage.currentApp.status)}
        </span>,
      )
    }
  }
  if (stage.state === 'error' && stage.failedApp) {
    lines.push(
      <span key="fail" style={{ color: C_ERROR }}>
        {stage.failedApp.name} · {tDynamic('status.' + stage.failedApp.status)}
      </span>,
    )
  }
  if (lines.length === 0) return null
  return (
    <div className="text-xs text-muted-foreground mt-0.5 flex flex-col gap-0.5">
      {lines}
    </div>
  )
}
