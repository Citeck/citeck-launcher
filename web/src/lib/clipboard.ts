import { toast } from './toast'
import { t } from './i18n'

/**
 * Legacy copy path for non-secure contexts: `navigator.clipboard` is
 * undefined when the UI is served over plain HTTP from a non-localhost host
 * (server mode on a LAN box), so fall back to the classic hidden-textarea +
 * `document.execCommand('copy')` dance.
 */
function legacyCopy(text: string, context: HTMLElement = document.body): boolean {
  const ta = document.createElement('textarea')
  ta.value = text
  ta.setAttribute('readonly', '')
  // Off-screen but still selectable; `display:none` would break select().
  ta.style.position = 'fixed'
  ta.style.left = '-9999px'
  ta.style.top = '0'
  const previousFocus = document.activeElement
  try {
    context.appendChild(ta)
    ta.focus({ preventScroll: true })
    ta.select()
    // An inert host (e.g. body behind showModal) can report copy success
    // without selecting the textarea, leaving the old clipboard untouched.
    if (document.activeElement !== ta) return false
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    ta.remove()
    if (previousFocus instanceof HTMLElement) previousFocus.focus({ preventScroll: true })
  }
}

/**
 * Copies `text` to the clipboard, working in BOTH secure and non-secure
 * contexts. Prefers the async Clipboard API when available (secure context),
 * falls back to the textarea/execCommand path otherwise, and shows an error
 * toast when every strategy fails — never a silent TypeError.
 *
 * For a button inside a native modal, pass its dialog as `context` so the
 * fallback textarea is selectable, not in the inert body behind the modal.
 * Other callers keep the default body host. Focus is restored after fallback.
 * Returns true when the text was copied.
 */
export async function copyText(text: string, context?: HTMLElement): Promise<boolean> {
  if (window.isSecureContext && navigator.clipboard) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      /* permission denied / transient failure — try the legacy path */
    }
  }
  if (legacyCopy(text, context)) return true
  toast(t('clipboard.copyFailed'), 'error')
  return false
}
