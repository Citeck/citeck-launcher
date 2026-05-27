import { t } from '../lib/i18n'

interface StatusBadgeProps {
  status: string
  /** "pill" (default, used in app table rows) or "indicator" (Kotlin sidebar — large filled circle + UPPERCASE name). */
  variant?: 'pill' | 'indicator'
}

// Kotlin parity (AppRuntimeStatus.kt) — category → hex. See docs/porting/02 §7.2.
const C_RUNNING = '#33AB50'
const C_TRANSIENT = '#F4E909' // STARTING / PULLING / STOPPING / READY_*
const C_STALLED = '#DB831D' // PULL_FAILED / START_FAILED / STOPPING_FAILED / STALLED / FAILED
const C_STOPPED = '#424242'

const statusColor: Record<string, string> = {
  RUNNING: C_RUNNING,
  HEALTHY: C_RUNNING,
  STARTING: C_TRANSIENT,
  PULLING: C_TRANSIENT,
  DEPS_WAITING: C_TRANSIENT,
  READY_TO_PULL: C_TRANSIENT,
  READY_TO_START: C_TRANSIENT,
  STOPPING: C_TRANSIENT,
  FAILED: C_STALLED,
  PULL_FAILED: C_STALLED,
  START_FAILED: C_STALLED,
  STOPPING_FAILED: C_STALLED,
  STALLED: C_STALLED,
  STOPPED: C_STOPPED,
}

// Statuses where the dot pulses (in-flight transitions, parity with Kotlin).
const PULSE_DOT = new Set(['STARTING', 'PULLING', 'DEPS_WAITING', 'STOPPING'])

export function StatusBadge({ status, variant = 'pill' }: StatusBadgeProps) {
  const color = statusColor[status] ?? C_STOPPED
  const label = t('status.' + status)
  const pulse = PULSE_DOT.has(status) ? 'animate-pulse' : ''
  if (variant === 'indicator') {
    return (
      <span className="inline-flex items-center gap-2 text-xs font-medium uppercase tracking-wide">
        <span
          className={`inline-block h-5 w-5 rounded-full border border-black/60 ${pulse}`}
          style={{ backgroundColor: color }}
        />
        <span style={{ color }}>{label}</span>
      </span>
    )
  }
  return (
    <span
      className="inline-flex items-center gap-1.5 rounded px-1.5 py-0 text-[11px] font-medium leading-5"
      style={{ backgroundColor: `${color}1A`, color }}
    >
      <span
        className={`inline-block h-1.5 w-1.5 rounded-full ${pulse}`}
        style={{ backgroundColor: color }}
      />
      {label}
    </span>
  )
}
