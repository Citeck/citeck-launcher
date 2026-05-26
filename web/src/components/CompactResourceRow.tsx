interface CompactResourceRowProps {
  label: string
  used: string
  total?: string
  percent: number // 0..100 — drives the bar fill and color thresholds
  throttled?: boolean
}

// Kotlin parity (ContainerStatViews.kt CompactResourceRow) — aggregate
// thresholds are more sensitive than per-app StatsCell (95/90).
const C_RED = '#E53935'
const C_ORANGE = '#FFA726'
const C_GREEN = '#66BB6A'

function barColor(p: number, throttled?: boolean): string {
  if (throttled) return C_ORANGE
  if (p >= 90) return C_RED
  if (p >= 70) return C_ORANGE
  return C_GREEN
}

export function CompactResourceRow({ label, used, total, percent, throttled }: CompactResourceRowProps) {
  const clamped = Math.max(0, Math.min(100, percent))
  const color = barColor(clamped, throttled)
  const valueText = total ? `${used} / ${total}` : used
  return (
    <div className="flex items-center gap-2 text-[11px] leading-4">
      <span className="text-muted-foreground w-7 shrink-0">{label}</span>
      <span className="font-mono flex-1 truncate" title={valueText}>{valueText}</span>
      <div className="h-1.5 w-20 rounded-full bg-muted overflow-hidden shrink-0">
        <div
          className="h-full rounded-full transition-all duration-300"
          style={{ width: `${clamped}%`, backgroundColor: color }}
        />
      </div>
    </div>
  )
}
