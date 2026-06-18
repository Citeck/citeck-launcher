import { useEffect, useLayoutEffect, useRef } from 'react'
import { createPortal } from 'react-dom'

export interface ContextMenuItem {
  label: string
  icon?: React.ReactNode
  // Inline marker rendered before the label (e.g. blue vertical bar for an
  // edited mounted file). Kept distinct from `icon` so callers can use both.
  decoration?: React.ReactNode
  onClick: () => void
  disabled?: boolean
  /** Native tooltip — set it to explain WHY a disabled item can't be used. */
  title?: string
  variant?: 'default' | 'danger'
  divider?: boolean
}

export interface ContextMenuProps {
  items: ContextMenuItem[]
  position: { x: number; y: number }
  onClose: () => void
}

export function ContextMenu({ items, position, onClose }: ContextMenuProps) {
  const ref = useRef<HTMLDivElement>(null)

  // Clamp the menu inside the viewport so it doesn't get clipped when the
  // anchor (e.g. a gear icon) sits near the right/bottom edge. Measured after
  // mount because we need the actual rendered size, not min-width.
  // Mutates the element's style directly to avoid a state-driven re-render.
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    const margin = 4
    let x = position.x
    let y = position.y
    if (x + rect.width > window.innerWidth - margin) {
      x = Math.max(margin, window.innerWidth - rect.width - margin)
    }
    if (y + rect.height > window.innerHeight - margin) {
      y = Math.max(margin, window.innerHeight - rect.height - margin)
    }
    el.style.left = `${x}px`
    el.style.top = `${y}px`
  }, [position])

  useEffect(() => {
    // Tracked at effect scope so cleanup can remove a pending swallow listener
    // that never fired (e.g. the dismissing mousedown was dragged away with no
    // matching click) — otherwise it would linger and eat the next global click.
    let swallow: ((ev: MouseEvent) => void) | null = null
    const clearSwallow = () => {
      if (swallow) {
        document.removeEventListener('click', swallow, true)
        swallow = null
      }
    }
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        onClose()
        // A left-button mousedown outside closes the menu, but the matching
        // `click` would still reach whatever is underneath (e.g. an app row,
        // opening the drawer). Swallow exactly that one click in the capture
        // phase — before React's handlers — so the first click only dismisses.
        if (e.button === 0) {
          clearSwallow() // drop any stale pending swallow first
          swallow = (ev: MouseEvent) => {
            ev.stopPropagation()
            ev.preventDefault()
            clearSwallow()
          }
          document.addEventListener('click', swallow, true)
        }
      }
    }
    function handleKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('mousedown', handleClick)
    document.addEventListener('keydown', handleKey)
    return () => {
      document.removeEventListener('mousedown', handleClick)
      document.removeEventListener('keydown', handleKey)
      clearSwallow()
    }
  }, [onClose])

  // Portal to <body>: a fixed-position overlay must not live inside the table
  // (a caller renders it among <tr>s) — an extra row there perturbs the
  // border-collapse layout and nudges row heights by ~1px when the menu opens.
  return createPortal(
    <div
      ref={ref}
      className="fixed z-50 bg-card border border-border rounded shadow-lg py-1 min-w-[160px]"
      style={{ left: position.x, top: position.y }}
    >
      {items.map((item, i) =>
        item.divider ? (
          <div key={`divider-${i}`} className="border-t border-border my-1" />
        ) : (
          // Wrap in a span carrying the title: a native tooltip on a disabled
          // <button> never shows (pointer events suppressed), and the disabled
          // restart item needs its "why" hint to appear.
          <span key={item.label} title={item.title} className="block">
            <button
              type="button"
              disabled={item.disabled}
              // card and muted are the same colour in this theme, so a
              // hover:bg-muted highlight was invisible — use accent (a step
              // lighter) so the hovered item actually reads as selected.
              className={`flex items-center gap-2 w-full text-left px-3 py-1.5 text-xs whitespace-nowrap transition-colors ${
                item.disabled
                  ? 'text-muted-foreground cursor-not-allowed'
                  : item.variant === 'danger'
                    ? 'text-destructive hover:bg-destructive/15'
                    : 'text-foreground hover:bg-accent'
              }`}
              onClick={() => {
                if (!item.disabled) {
                  item.onClick()
                  onClose()
                }
              }}
            >
              {item.icon && <span className="w-4 h-4 flex items-center justify-center">{item.icon}</span>}
              {item.decoration}
              {item.label}
            </button>
          </span>
        ),
      )}
    </div>,
    document.body,
  )
}
