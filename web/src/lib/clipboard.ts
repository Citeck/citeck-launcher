import { toast } from './toast'
import { t } from './i18n'

/**
 * Legacy copy path for non-secure contexts: `navigator.clipboard` is
 * undefined when the UI is served over plain HTTP from a non-localhost host
 * (server mode on a LAN box), so fall back to the classic hidden-textarea +
 * `document.execCommand('copy')` dance.
 */
function legacyCopy(text: string): boolean {
  const ta = document.createElement('textarea')
  ta.value = text
  ta.setAttribute('readonly', '')
  // Off-screen but still selectable; `display:none` would break select().
  ta.style.position = 'fixed'
  ta.style.left = '-9999px'
  ta.style.top = '0'
  document.body.appendChild(ta)
  ta.select()
  let ok: boolean
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  document.body.removeChild(ta)
  return ok
}

/**
 * Copies `text` to the clipboard, working in BOTH secure and non-secure
 * contexts. Prefers the async Clipboard API when available (secure context),
 * falls back to the textarea/execCommand path otherwise, and shows an error
 * toast when every strategy fails — never a silent TypeError.
 *
 * Returns true when the text was copied.
 */
export async function copyText(text: string): Promise<boolean> {
  if (window.isSecureContext && navigator.clipboard) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      /* permission denied / transient failure — try the legacy path */
    }
  }
  if (legacyCopy(text)) return true
  toast(t('clipboard.copyFailed'), 'error')
  return false
}
