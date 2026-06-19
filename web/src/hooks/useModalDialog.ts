import { useEffect, useRef } from 'react'

/**
 * Single source of truth for driving a native `<dialog>` as a modal.
 *
 * Every modal dialog in the app used to inline the same effect — mirror the
 * `open` prop into `showModal()` / `close()` — copy-pasted across ~9 components.
 * This hook owns that logic in one place. Attach the returned ref to the
 * `<dialog>` element and pass the controlled `open` flag.
 *
 * Critically, it also **closes the dialog on unmount**. A modal `<dialog>`
 * removed from the DOM while still `.open` leaks its top-layer / `::backdrop`
 * on Chromium / WebView2 (the Windows desktop webview): an invisible backdrop
 * keeps intercepting every click and the whole launcher window becomes
 * unclickable until restart. WebKitGTK (the Linux webview) tolerates it, which
 * is why the bug only reproduced on Windows. Closing on unmount makes the leak
 * impossible regardless of how the consumer tears the dialog down (toggling
 * `open` to false OR unmounting it outright, e.g. when a parent dialog in a
 * nested stack closes).
 *
 * @param open controlled visibility flag
 * @returns a ref to attach to the `<dialog>` element
 */
export function useModalDialog(open: boolean) {
  const ref = useRef<HTMLDialogElement>(null)

  // Mirror `open` into the native modal state.
  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    else if (!open && dialog.open) dialog.close()
  }, [open])

  // Close on unmount regardless of `open` — prevents the Chromium/WebView2
  // top-layer leak when the dialog is removed from the DOM while still open.
  // Separate []-deps effect so it fires only at unmount, not on every toggle
  // (a toggle-coupled cleanup would close+reopen and spuriously fire onClose).
  useEffect(() => {
    const dialog = ref.current
    return () => {
      if (dialog?.open) dialog.close()
    }
  }, [])

  return ref
}
