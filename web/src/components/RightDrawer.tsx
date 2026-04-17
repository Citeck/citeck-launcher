import { useEffect, useRef, useState, type ReactNode } from 'react'
import { X } from 'lucide-react'
import { useTranslation } from '../lib/i18n'

interface RightDrawerProps {
  title: string
  subtitle?: ReactNode
  onClose: () => void
  children: ReactNode
}

export function RightDrawer({ title, subtitle, onClose, children }: RightDrawerProps) {
  const { t } = useTranslation()
  const panelRef = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)

  // Slide-in animation on mount
  useEffect(() => {
    requestAnimationFrame(() => setOpen(true))
  }, [])

  return (
    <div className="absolute inset-0 z-10 flex justify-end">
      {/* Backdrop */}
      <div className="absolute inset-0 bg-black/25 backdrop-blur-[1px]" onClick={onClose} />
      {/* Drawer panel */}
      <div
        ref={panelRef}
        className="relative w-[420px] max-w-[85%] bg-card border-l border-border shadow-xl flex flex-col transition-transform duration-200 ease-out"
        style={{ transform: open ? 'translateX(0)' : 'translateX(100%)' }}
      >
        {/* Header */}
        <div className="flex items-center justify-between px-3 py-2 border-b border-border shrink-0">
          <div className="min-w-0">
            <div className="text-sm font-semibold truncate">{title}</div>
            {subtitle && <div className="text-[11px] text-muted-foreground truncate">{subtitle}</div>}
          </div>
          <button
            type="button"
            className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted shrink-0"
            onClick={onClose}
            aria-label={t('common.close')}
            title={t('common.close')}
          >
            <X size={16} />
          </button>
        </div>
        {/* Body */}
        <div className="flex-1 min-h-0 overflow-y-auto p-3">
          {children}
        </div>
      </div>
    </div>
  )
}
