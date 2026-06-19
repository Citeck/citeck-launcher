interface StatsCellProps {
  text: string
  percent: number
  isActive: boolean
  isWarning?: boolean
  isCritical?: boolean
  title?: string
  align?: 'left' | 'right'
}

// Kotlin StatsCell parity (ContainerStatViews.kt) — per-app uses warning/critical
// flags already computed on the DTO (memoryWarning 80%, memoryCritical 95%, CPU
// throttled flag). The aggregate CompactResourceRow keeps its own 90/70 percent
// thresholds.
const C_RED = '#E53935'
const C_ORANGE = '#FFA726'
const C_GREEN = '#66BB6A'

function color(isWarning?: boolean, isCritical?: boolean): string {
  if (isCritical) return C_RED
  if (isWarning) return C_ORANGE
  return C_GREEN
}

export function StatsCell({ text, percent, isActive, isWarning, isCritical, title, align = 'right' }: StatsCellProps) {
  if (!isActive) {
    return <span className="text-muted-foreground">-</span>
  }
  const clamped = Math.max(0, Math.min(100, percent))
  const bar = color(isWarning, isCritical)
  const textColor = isCritical ? 'text-red-500' : isWarning ? 'text-amber-500' : 'text-muted-foreground'
  return (
    <span
      // select-none: CPU/MEM values refresh ~every 2s; without it an accidental
      // text-selection drag leaves a "stuck" selection highlight on these cells
      // because the selected text nodes get replaced under the browser's
      // selection range on each stats update.
      className={`inline-flex flex-col gap-px leading-none select-none ${align === 'right' ? 'items-end' : 'items-start'}`}
      title={title}
    >
      <span className={`font-mono leading-none ${textColor}`}>{text}</span>
      <span className="block h-[3px] w-[56px] rounded-full bg-muted overflow-hidden">
        <span
          className="block h-full rounded-full transition-[width] duration-300"
          style={{ width: `${clamped}%`, backgroundColor: bar }}
        />
      </span>
    </span>
  )
}
