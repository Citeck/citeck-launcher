interface StatusBadgeProps {
  status: string
}

const statusStyles: Record<string, string> = {
  RUNNING: 'bg-success/15 text-success',
  HEALTHY: 'bg-success/15 text-success',
  FAILED: 'bg-destructive/15 text-destructive',
  PULL_FAILED: 'bg-destructive/15 text-destructive',
  START_FAILED: 'bg-destructive/15 text-destructive',
  STOPPING_FAILED: 'bg-destructive/15 text-destructive',
  STARTING: 'bg-warning/15 text-warning',
  PULLING: 'bg-warning/15 text-warning',
  DEPS_WAITING: 'bg-warning/15 text-warning',
  READY_TO_PULL: 'bg-warning/15 text-warning',
  READY_TO_START: 'bg-warning/15 text-warning',
  STOPPED: 'bg-muted text-muted-foreground',
  STALLED: 'bg-destructive/15 text-destructive',
}

export function StatusBadge({ status }: StatusBadgeProps) {
  const style = statusStyles[status] ?? 'bg-muted text-muted-foreground'
  return (
    <span className={`inline-flex items-center rounded px-1.5 py-0 text-[11px] font-medium leading-5 ${style}`}>
      {status}
    </span>
  )
}
