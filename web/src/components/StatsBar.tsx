import type { AppDto } from '../lib/types'

interface StatsBarProps {
  apps: AppDto[]
}

export function StatsBar({ apps }: StatsBarProps) {
  const total = apps.length
  const running = apps.filter((a) => a.status === 'RUNNING').length
  const failed = apps.filter((a) =>
    ['FAILED', 'START_FAILED', 'PULL_FAILED', 'STOPPING_FAILED'].includes(a.status),
  ).length
  const starting = apps.filter((a) =>
    ['STARTING', 'PULLING', 'DEPS_WAITING', 'READY_TO_PULL', 'READY_TO_START'].includes(a.status),
  ).length
  const stopped = apps.filter((a) => a.status === 'STOPPED').length

  return (
    <div className="flex items-center gap-4 rounded-lg border border-border bg-card px-4 py-3">
      <Stat label="Total" value={total} />
      <Divider />
      <Stat label="Running" value={running} color="text-success" />
      {starting > 0 && (
        <>
          <Divider />
          <Stat label="Starting" value={starting} color="text-warning" />
        </>
      )}
      {failed > 0 && (
        <>
          <Divider />
          <Stat label="Failed" value={failed} color="text-destructive" />
        </>
      )}
      {stopped > 0 && (
        <>
          <Divider />
          <Stat label="Stopped" value={stopped} color="text-muted-foreground" />
        </>
      )}
    </div>
  )
}

function Stat({ label, value, color }: { label: string; value: number; color?: string }) {
  return (
    <div className="text-center">
      <div className={`text-lg font-semibold ${color ?? 'text-foreground'}`}>{value}</div>
      <div className="text-xs text-muted-foreground">{label}</div>
    </div>
  )
}

function Divider() {
  return <div className="h-8 w-px bg-border" />
}
