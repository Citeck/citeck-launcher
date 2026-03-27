import { useToastStore } from '../lib/toast'
import type { ToastType } from '../lib/toast'
import { X } from 'lucide-react'

const typeStyles: Record<ToastType, string> = {
  success: 'border-green-600/40 bg-green-950/80 text-green-300',
  error: 'border-red-600/40 bg-red-950/80 text-red-300',
  info: 'border-blue-600/40 bg-blue-950/80 text-blue-300',
}

export function ToastContainer() {
  const toasts = useToastStore((s) => s.toasts)
  const removeToast = useToastStore((s) => s.removeToast)

  if (toasts.length === 0) return null

  return (
    <div className="fixed bottom-4 right-4 z-50 flex flex-col gap-2 max-w-xs">
      {toasts.map((t) => (
        <div key={t.id}
          className={`flex items-start gap-2 rounded border px-3 py-2 text-xs shadow-lg backdrop-blur-sm ${typeStyles[t.type]}`}>
          <span className="flex-1">{t.message}</span>
          <button onClick={() => removeToast(t.id)} className="shrink-0 opacity-60 hover:opacity-100">
            <X size={12} />
          </button>
        </div>
      ))}
    </div>
  )
}
