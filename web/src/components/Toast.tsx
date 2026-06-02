import { useToastStore } from '../lib/toast'
import type { ToastType } from '../lib/toast'
import { X } from 'lucide-react'

// Type is conveyed by border colour + a thicker coloured left accent bar. The
// surface itself stays the opaque card colour so the message text sits at full
// foreground contrast — the old `bg-*/10` + backdrop-blur washed the text out
// to near-unreadable on the dark theme.
const typeStyles: Record<ToastType, string> = {
  success: 'border-success/40 border-l-success',
  error: 'border-destructive/40 border-l-destructive',
  info: 'border-primary/40 border-l-primary',
}

const accentText: Record<ToastType, string> = {
  success: 'text-success',
  error: 'text-destructive',
  info: 'text-primary',
}

export function ToastContainer() {
  const toasts = useToastStore((s) => s.toasts)
  const removeToast = useToastStore((s) => s.removeToast)

  if (toasts.length === 0) return null

  return (
    <div className="fixed top-4 right-4 z-50 flex flex-col gap-2 max-w-sm">
      {toasts.map((t) => (
        <div key={t.id}
          className={`flex items-start gap-2 rounded-md border border-l-[3px] bg-card text-card-foreground px-3 py-2 text-xs shadow-lg ${typeStyles[t.type]}`}>
          <span className="flex-1 break-words">{t.message}</span>
          <button onClick={() => removeToast(t.id)} className={`shrink-0 opacity-60 hover:opacity-100 ${accentText[t.type]}`}>
            <X size={12} />
          </button>
        </div>
      ))}
    </div>
  )
}
