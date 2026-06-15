import { tDynamic } from '../lib/i18n'

interface StatusBadgeProps {
  status: string
  /** "pill" (default, used in app table rows) or "indicator" (Kotlin sidebar — large filled circle + UPPERCASE name). */
  variant?: 'pill' | 'indicator'
}

// Status → theme-aware CSS color token (defined in index.css). Using a var
// instead of a fixed hex lets the same status read correctly on both the dark
// and light surfaces — see the --color-status-* tokens.
const C_RUNNING = 'var(--color-status-running)'
const C_TRANSIENT = 'var(--color-status-transient)' // STARTING / PULLING / STOPPING / UPDATING / READY_*
const C_STALLED = 'var(--color-status-stalled)' // PULL_FAILED / START_FAILED / STOPPING_FAILED / STALLED / FAILED
const C_STOPPED = 'var(--color-status-stopped)'

const statusColor: Record<string, string> = {
  RUNNING: C_RUNNING,
  HEALTHY: C_RUNNING,
  STARTING: C_TRANSIENT,
  PULLING: C_TRANSIENT,
  DEPS_WAITING: C_TRANSIENT,
  READY_TO_PULL: C_TRANSIENT,
  READY_TO_START: C_TRANSIENT,
  STOPPING: C_TRANSIENT,
  UPDATING: C_TRANSIENT,
  FAILED: C_STALLED,
  PULL_FAILED: C_STALLED,
  START_FAILED: C_STALLED,
  STOPPING_FAILED: C_STALLED,
  STALLED: C_STALLED,
  STOPPED: C_STOPPED,
}

// Statuses where the dot pulses (in-flight transitions, parity with Kotlin).
const PULSE_DOT = new Set(['STARTING', 'PULLING', 'DEPS_WAITING', 'STOPPING', 'UPDATING'])

export function StatusBadge({ status, variant = 'pill' }: StatusBadgeProps) {
  const color = statusColor[status] ?? C_STOPPED
  // Runtime-assembled key from the daemon's status string — sanctioned
  // tDynamic escape hatch (unknown statuses render as the raw key).
  const label = tDynamic('status.' + status)
  const pulse = PULSE_DOT.has(status) ? 'animate-pulse' : ''
  if (variant === 'indicator') {
    return (
      <span className="inline-flex items-center gap-2 text-xs font-medium uppercase tracking-wide">
        <span
          className={`inline-block h-3.5 w-3.5 rounded-full ring-2 ring-inset ring-black/15 ${pulse}`}
          style={{ backgroundColor: color, boxShadow: `0 0 0 3px color-mix(in srgb, ${color} 22%, transparent)` }}
        />
        <span style={{ color }}>{label}</span>
      </span>
    )
  }
  return (
    <span
      className="inline-flex items-center gap-1.5 rounded px-1.5 py-0 text-[11px] font-medium leading-[17px]"
      style={{ backgroundColor: `color-mix(in srgb, ${color} 14%, transparent)`, color }}
    >
      <span
        className={`inline-block h-1.5 w-1.5 rounded-full ${pulse}`}
        style={{ backgroundColor: color }}
      />
      {label}
    </span>
  )
}
