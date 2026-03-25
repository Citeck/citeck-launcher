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
    <div className="flex items-center gap-3 rounded border border-border bg-card px-3 py-1.5 text-xs">
      <Stat label="Total" value={total} />
      <Sep />
      <Stat label="Running" value={running} color="text-success" />
      {starting > 0 && <><Sep /><Stat label="Starting" value={starting} color="text-warning" /></>}
      {failed > 0 && <><Sep /><Stat label="Failed" value={failed} color="text-destructive" /></>}
      {stopped > 0 && <><Sep /><Stat label="Stopped" value={stopped} color="text-muted-foreground" /></>}
    </div>
  )
}

function Stat({ label, value, color }: { label: string; value: number; color?: string }) {
  return (
    <span>
      <span className={`font-semibold ${color ?? 'text-foreground'}`}>{value}</span>
      <span className="ml-1 text-muted-foreground">{label}</span>
    </span>
  )
}

function Sep() {
  return <span className="text-border">·</span>
}
