import { X } from 'lucide-react'
import { useModalDialog } from '../hooks/useModalDialog'

interface ModalProps {
  open: boolean
  title: string
  onClose: () => void
  /** sm = 360px, md = 480px (default), lg = 640px, xl = 920px. All capped at 90vw. */
  width?: 'sm' | 'md' | 'lg' | 'xl'
  /** Body of the dialog — fields, content, etc. Padded + scrollable. */
  children: React.ReactNode
  /** Optional footer slot — typically a Cancel / Save button row.
   *  Rendered with the same horizontal padding as the header/body. */
  footer?: React.ReactNode
  /** Form submit handler — when set, the body+footer are wrapped in a
   *  <form>, so Enter submits and the footer's submit button works. */
  onSubmit?: (e: React.FormEvent) => void
}

/**
 * Single source of truth for centered modal dialogs. Solves three recurring
 * problems individual ad-hoc dialogs kept tripping on:
 *   1. **Center positioning**: `fixed inset-0 m-auto` with an explicit width
 *      makes a native `<dialog>` actually sit in the viewport center under
 *      `showModal()`. Most ad-hoc dialogs got this wrong.
 *   2. **Theme tokens**: `bg-card` + `border-border` + `text-foreground` so
 *      the dark theme is applied; `bg-popover` / undefined tokens left the
 *      dialog with the user-agent's white box.
 *   3. **Layout consistency**: every modal gets the same header (title +
 *      close X), scrollable body, and optional footer row. Padding,
 *      borders, and the close affordance are wired in one place.
 *
 * Use `<ModalField>` for label-wrapped inputs inside the body so spacing
 * and required-asterisk styling stay uniform across dialogs.
 */
export function Modal({ open, title, onClose, width = 'md', children, footer, onSubmit }: ModalProps) {
  const ref = useModalDialog(open)

  const widthClass =
    width === 'sm' ? 'w-[360px]'
      : width === 'lg' ? 'w-[640px]'
        : width === 'xl' ? 'w-[920px]'
          : 'w-[480px]'

  const body = (
    <>
      <div className="flex items-center justify-between border-b border-border px-4 py-2.5">
        <h2 className="text-sm font-semibold">{title}</h2>
        <button
          type="button"
          className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
          onClick={onClose}
          aria-label="Close"
        >
          <X size={14} />
        </button>
      </div>
      <div className="space-y-3 px-4 py-3 max-h-[70vh] overflow-y-auto">
        {children}
      </div>
      {footer && (
        <div className="flex items-center justify-between border-t border-border px-4 py-2.5">
          {footer}
        </div>
      )}
    </>
  )

  return (
    <dialog
      ref={ref}
      // Only react to THIS dialog's own close event. A nested modal (e.g. a
      // SecretEditDialog opened from a field inside this dialog) fires its own
      // native `close`, and React bubbles that event up the fiber tree to this
      // ancestor <dialog>'s onClose — which would otherwise close the dialog
      // behind it too. currentTarget is this <dialog>; target is whatever
      // actually closed.
      onClose={(e) => { if (e.target === e.currentTarget) onClose() }}
      className={`fixed inset-0 z-50 m-auto ${widthClass} max-w-[90vw] rounded-lg border border-border bg-card p-0 text-foreground shadow-xl`}
    >
      {onSubmit ? (
        <form onSubmit={onSubmit} className="flex flex-col">{body}</form>
      ) : (
        <div className="flex flex-col">{body}</div>
      )}
    </dialog>
  )
}

interface ModalFieldProps {
  label: string
  required?: boolean
  error?: string
  children: React.ReactNode
}

/** Label-wrapped form row. The asterisk and error styling are uniform across
 *  every modal that uses it — pair with the standard input class:
 *  `w-full rounded border border-border bg-background px-2.5 py-1.5 text-sm
 *   focus:outline-none focus:border-primary`. */
export function ModalField({ label, required, error, children }: ModalFieldProps) {
  return (
    <div>
      <label className="block text-xs font-medium mb-1">
        {label}
        {required && <span className="ml-0.5 text-destructive">*</span>}
      </label>
      {children}
      {error && <div className="mt-1 text-[11px] text-destructive">{error}</div>}
    </div>
  )
}
