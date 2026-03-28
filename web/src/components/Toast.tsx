import { useToastStore } from '../lib/toast'
import type { ToastType } from '../lib/toast'
import { X } from 'lucide-react'

const typeStyles: Record<ToastType, string> = {
  success: 'border-success/40 bg-success/10 text-success',
  error: 'border-destructive/40 bg-destructive/10 text-destructive',
  info: 'border-primary/40 bg-primary/10 text-primary',
}

export function ToastContainer() {
  const toasts = useToastStore((s) => s.toasts)
  const removeToast = useToastStore((s) => s.removeToast)

  if (toasts.length === 0) return null

  return (
    <div className="fixed bottom-4 right-4 z-50 flex flex-col gap-2 max-w-xs">
      {toasts.map((t) => (
        <div key={t.id}
          className={`flex items-start gap-2 rounded-md border px-3 py-2 text-xs shadow-lg backdrop-blur-sm ${typeStyles[t.type]}`}>
          <span className="flex-1">{t.message}</span>
          <button onClick={() => removeToast(t.id)} className="shrink-0 opacity-60 hover:opacity-100">
            <X size={12} />
          </button>
        </div>
      ))}
    </div>
  )
}
