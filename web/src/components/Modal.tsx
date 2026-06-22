import { createPortal } from 'react-dom'
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

  // React replays a nested modal's <form> submit up the FIBER tree (portaling
  // the dialog to <body> moves the DOM node but not the React-tree position), so
  // without this guard the inner form's submit would also fire this outer form's
  // onSubmit. Mirror the onClose target guard: only handle THIS form's own submit
  // and stop it here so no ancestor form sees a nested submit. (The DOM-level
  // nested-form problem — Chromium resolves a nested submit button's form owner
  // to the OUTERMOST form — is solved separately by the body portal below.)
  const handleSubmit = onSubmit
    ? (e: React.FormEvent) => {
        if (e.target !== e.currentTarget) return
        e.stopPropagation()
        onSubmit(e)
      }
    : undefined

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

  // Portal the <dialog> to <body> so a modal opened from INSIDE another modal is
  // never a DOM descendant of the outer modal's <form>. Nested <form>s are
  // invalid HTML, and Chromium/WebView2 resolves a nested submit button's form
  // owner to the OUTERMOST form — so clicking "create" in an inner dialog would
  // submit the OUTER form (e.g. the workspace-create form), collapsing the whole
  // stack and running the wrong handler. As a body child each dialog's form is
  // standalone, so the submit button binds to its own form. Theme tokens live on
  // <html data-theme>, which <body> inherits, so colors are unaffected.
  return createPortal(
    <dialog
      ref={ref}
      // Only react to THIS dialog's own close event. A nested modal fires its own
      // native `close`, and React replays it up the fiber tree to this ancestor
      // <dialog>'s onClose — which would otherwise close the dialog behind it too.
      // currentTarget is this <dialog>; target is whatever actually closed.
      onClose={(e) => { if (e.target === e.currentTarget) onClose() }}
      className={`fixed inset-0 z-50 m-auto ${widthClass} max-w-[90vw] rounded-lg border border-border bg-card p-0 text-foreground shadow-xl`}
    >
      {handleSubmit ? (
        <form onSubmit={handleSubmit} className="flex flex-col">{body}</form>
      ) : (
        <div className="flex flex-col">{body}</div>
      )}
    </dialog>,
    document.body,
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
