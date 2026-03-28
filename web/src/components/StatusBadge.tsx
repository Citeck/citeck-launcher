interface StatusBadgeProps {
  status: string
}

const statusStyles: Record<string, { bg: string; text: string; dot: string }> = {
  RUNNING: { bg: 'bg-success/10', text: 'text-success', dot: 'bg-success' },
  HEALTHY: { bg: 'bg-success/10', text: 'text-success', dot: 'bg-success' },
  FAILED: { bg: 'bg-destructive/10', text: 'text-destructive', dot: 'bg-destructive' },
  PULL_FAILED: { bg: 'bg-destructive/10', text: 'text-destructive', dot: 'bg-destructive' },
  START_FAILED: { bg: 'bg-destructive/10', text: 'text-destructive', dot: 'bg-destructive' },
  STOPPING_FAILED: { bg: 'bg-destructive/10', text: 'text-destructive', dot: 'bg-destructive' },
  STARTING: { bg: 'bg-warning/10', text: 'text-warning', dot: 'bg-warning animate-pulse' },
  PULLING: { bg: 'bg-warning/10', text: 'text-warning', dot: 'bg-warning animate-pulse' },
  DEPS_WAITING: { bg: 'bg-warning/10', text: 'text-warning', dot: 'bg-warning animate-pulse' },
  READY_TO_PULL: { bg: 'bg-warning/10', text: 'text-warning', dot: 'bg-warning' },
  READY_TO_START: { bg: 'bg-warning/10', text: 'text-warning', dot: 'bg-warning' },
  STOPPING: { bg: 'bg-warning/10', text: 'text-warning', dot: 'bg-warning animate-pulse' },
  STOPPED: { bg: 'bg-muted', text: 'text-muted-foreground', dot: 'bg-muted-foreground' },
  STALLED: { bg: 'bg-destructive/10', text: 'text-destructive', dot: 'bg-destructive' },
}

const displayNames: Record<string, string> = {
  DEPS_WAITING: 'Waiting',
  READY_TO_PULL: 'Queued',
  READY_TO_START: 'Ready',
  PULL_FAILED: 'Pull Failed',
  START_FAILED: 'Start Failed',
  STOPPING_FAILED: 'Stop Failed',
}

const defaultStyle = { bg: 'bg-muted', text: 'text-muted-foreground', dot: 'bg-muted-foreground' }

export function StatusBadge({ status }: StatusBadgeProps) {
  const s = statusStyles[status] ?? defaultStyle
  const label = displayNames[status] ?? status
  return (
    <span className={`inline-flex items-center gap-1.5 rounded px-1.5 py-0 text-[11px] font-medium leading-5 ${s.bg} ${s.text}`}>
      <span className={`inline-block h-1.5 w-1.5 rounded-full ${s.dot}`} />
      {label}
    </span>
  )
}
