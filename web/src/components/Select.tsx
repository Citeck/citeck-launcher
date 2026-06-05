import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ChevronDown, X } from 'lucide-react'

export interface SelectOption {
  label: string
  value: string
}

interface SelectProps {
  value: string
  options: SelectOption[]
  onChange: (value: string) => void
  disabled?: boolean
  required?: boolean
  placeholder?: string
  className?: string
}

const ITEM_HEIGHT = 30
const MAX_VISIBLE_ITEMS = 8

interface PopupPos {
  left: number
  width: number
  top?: number
  bottom?: number
}

/**
 * Custom select component:
 * - Clear (X) button shown when `!required && value !== ''`.
 * - Popup max-height is 8 × item-height.
 * - All options are listed; the currently selected one is highlighted.
 *   (Earlier versions hid the selected option to match Kotlin parity, but
 *   that made a single-option select uncloseable / unclickable — the
 *   trigger would refuse to open since `popupOptions` was empty.)
 * - Outside click / Escape closes the popup.
 * - The popup is rendered through a portal into <body> with fixed positioning
 *   derived from the trigger's rect, so it is never clipped by an ancestor
 *   with `overflow` (dialog body, scroll panel, wizard step). It flips above
 *   the trigger when there isn't room below.
 */
export function Select({
  value,
  options,
  onChange,
  disabled = false,
  required = false,
  placeholder,
  className = '',
}: SelectProps) {
  const [open, setOpen] = useState(false)
  const [activeIdx, setActiveIdx] = useState(-1)
  const [popupPos, setPopupPos] = useState<PopupPos | null>(null)
  const [portalEl, setPortalEl] = useState<HTMLElement | null>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const popupRef = useRef<HTMLDivElement>(null)

  const selectedOption = options.find((o) => o.value === value)
  const popupOptions = options

  useEffect(() => {
    if (!open) return
    function onDocClick(e: MouseEvent) {
      const target = e.target as Node
      // The popup lives in a portal (outside rootRef), so a click on an option
      // must NOT count as "outside" — otherwise mousedown would close the popup
      // before the option's click handler runs and the selection would be lost.
      if (rootRef.current?.contains(target) || popupRef.current?.contains(target)) return
      setOpen(false)
    }
    // The popup is fixed-positioned from a snapshot of the trigger rect; if the
    // page scrolls or resizes while it's open the anchor moves, so just close it.
    // But ignore scrolling INSIDE the popup's own option list.
    function onReposition(e: Event) {
      if (e.type === 'scroll' && e.target instanceof Node && popupRef.current?.contains(e.target)) return
      setOpen(false)
    }
    document.addEventListener('mousedown', onDocClick)
    window.addEventListener('resize', onReposition)
    window.addEventListener('scroll', onReposition, true) // capture: any ancestor scroll
    return () => {
      document.removeEventListener('mousedown', onDocClick)
      window.removeEventListener('resize', onReposition)
      window.removeEventListener('scroll', onReposition, true)
    }
  }, [open])

  function openPopup() {
    if (!triggerRef.current) return
    // Portal target: when the Select lives inside an open modal <dialog>
    // (showModal), the dialog is promoted to the browser top layer and
    // everything outside its subtree becomes inert + painted below the
    // backdrop. A popup portaled into document.body would then be invisible
    // and click-intercepted by the dialog. So portal into the nearest OPEN
    // <dialog> ancestor when there is one; otherwise into <body> (the popup
    // still escapes any overflow-clipping ancestor via position: fixed).
    const dlg = triggerRef.current.closest('dialog')
    setPortalEl(dlg && dlg.open ? dlg : document.body)
    const rect = triggerRef.current.getBoundingClientRect()
    const popupHeight = Math.min(popupOptions.length, MAX_VISIBLE_ITEMS) * ITEM_HEIGHT + 4
    const spaceBelow = window.innerHeight - rect.bottom
    const spaceAbove = rect.top
    // Flip above the trigger when a downward popup would be clipped by the
    // viewport bottom and there's more room above.
    const up = spaceBelow < popupHeight && spaceAbove > spaceBelow
    setPopupPos({
      left: rect.left,
      width: rect.width,
      top: up ? undefined : rect.bottom + 4,
      bottom: up ? window.innerHeight - rect.top + 4 : undefined,
    })
  }

  function onKeyDown(e: React.KeyboardEvent) {
    if (disabled) return
    if (!open) {
      if (e.key === 'Enter' || e.key === ' ' || e.key === 'ArrowDown') {
        e.preventDefault()
        openPopup()
        setOpen(true)
        setActiveIdx(0)
      }
      return
    }
    if (e.key === 'Escape') {
      e.preventDefault()
      setOpen(false)
      triggerRef.current?.focus()
    } else if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActiveIdx((i) => Math.min(i + 1, popupOptions.length - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActiveIdx((i) => Math.max(i - 1, 0))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      if (activeIdx >= 0 && activeIdx < popupOptions.length) {
        const opt = popupOptions[activeIdx]
        onChange(opt.value)
        setOpen(false)
        triggerRef.current?.focus()
      }
    }
  }

  const showClear = !required && value !== '' && !disabled

  return (
    <div ref={rootRef} className={`relative ${className}`} onKeyDown={onKeyDown}>
      <button
        ref={triggerRef}
        type="button"
        className="flex w-full items-center rounded border border-border bg-background px-2.5 py-1.5 text-left text-sm focus:outline-none focus:border-primary disabled:opacity-50"
        disabled={disabled}
        onClick={() => {
          if (popupOptions.length === 0) return
          if (!open) openPopup()
          setOpen((o) => !o)
          // Start the active highlight on the currently-selected option so
          // keyboard Enter is a safe no-op confirm.
          const selIdx = popupOptions.findIndex((o) => o.value === value)
          setActiveIdx(selIdx >= 0 ? selIdx : 0)
        }}
      >
        <span className="flex-1 truncate">
          {selectedOption ? selectedOption.label : (
            <span className="text-muted-foreground">{placeholder ?? ''}</span>
          )}
        </span>
        {showClear && (
          <span
            role="button"
            tabIndex={-1}
            className="mr-1 inline-flex h-5 w-5 items-center justify-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground"
            onClick={(e) => {
              e.stopPropagation()
              onChange('')
            }}
            onMouseDown={(e) => e.preventDefault()}
            title="Clear"
            aria-label="Clear"
          >
            <X size={12} />
          </span>
        )}
        <ChevronDown size={16} className="text-muted-foreground" />
      </button>

      {open && popupOptions.length > 0 && popupPos && createPortal(
        <div
          ref={popupRef}
          className="fixed z-50 overflow-y-auto rounded border border-border bg-card shadow-lg"
          style={{
            left: popupPos.left,
            width: popupPos.width,
            top: popupPos.top,
            bottom: popupPos.bottom,
            maxHeight: ITEM_HEIGHT * MAX_VISIBLE_ITEMS,
          }}
          role="listbox"
        >
          {popupOptions.map((opt, idx) => {
            const isSelected = opt.value === value
            return (
              <button
                key={opt.value}
                type="button"
                role="option"
                aria-selected={isSelected}
                className={`block w-full truncate px-2.5 py-1.5 text-left text-sm hover:bg-muted ${
                  idx === activeIdx ? 'bg-muted' : ''
                } ${isSelected ? 'text-primary font-medium' : ''}`}
                onMouseEnter={() => setActiveIdx(idx)}
                onClick={() => {
                  onChange(opt.value)
                  setOpen(false)
                  triggerRef.current?.focus()
                }}
              >
                {opt.label}
              </button>
            )
          })}
        </div>,
        portalEl ?? document.body,
      )}
    </div>
  )
}
