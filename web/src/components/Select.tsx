import { useEffect, useRef, useState } from 'react'
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

/**
 * Custom select component:
 * - Clear (X) button shown when `!required && value !== ''`.
 * - Popup max-height is 8 × item-height.
 * - All options are listed; the currently selected one is highlighted.
 *   (Earlier versions hid the selected option to match Kotlin parity, but
 *   that made a single-option select uncloseable / unclickable — the
 *   trigger would refuse to open since `popupOptions` was empty.)
 * - Outside click / Escape closes the popup.
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
  // `flipUp`: true when there's more room above the trigger than below, e.g.
  // the field sits near the dialog footer and a downward popup would be
  // clipped by the buttons row. Recomputed every time the dropdown opens.
  const [flipUp, setFlipUp] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)

  const selectedOption = options.find((o) => o.value === value)
  const popupOptions = options

  useEffect(() => {
    if (!open) return
    function onDocClick(e: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', onDocClick)
    return () => document.removeEventListener('mousedown', onDocClick)
  }, [open])

  function onKeyDown(e: React.KeyboardEvent) {
    if (disabled) return
    if (!open) {
      if (e.key === 'Enter' || e.key === ' ' || e.key === 'ArrowDown') {
        e.preventDefault()
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
          setOpen((o) => {
            const next = !o
            if (next && triggerRef.current) {
              // Decide flip direction at open-time so the popup never gets
              // covered by the dialog footer. Estimated height = visible
              // items × row height + 4px slack for the border/padding.
              const rect = triggerRef.current.getBoundingClientRect()
              const popupHeight = Math.min(popupOptions.length, MAX_VISIBLE_ITEMS) * ITEM_HEIGHT + 4
              const spaceBelow = window.innerHeight - rect.bottom
              const spaceAbove = rect.top
              setFlipUp(spaceBelow < popupHeight && spaceAbove > spaceBelow)
            }
            return next
          })
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

      {open && popupOptions.length > 0 && (
        <div
          className={`absolute left-0 right-0 z-50 overflow-y-auto rounded border border-border bg-card shadow-lg ${
            flipUp ? 'bottom-full mb-1' : 'top-full mt-1'
          }`}
          style={{ maxHeight: ITEM_HEIGHT * MAX_VISIBLE_ITEMS }}
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
        </div>
      )}
    </div>
  )
}
