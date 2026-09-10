import type { EventDto } from './types'
import { currentLocale } from './i18n'

export type EventHandler = (event: EventDto) => void
export type ResyncHandler = () => void

// SSE-based event stream (Server-Sent Events, no external deps).
// `lastSeq` triggers server-side replay from the daemon ring buffer; the
// daemon also reads the standard Last-Event-ID header, but EventSource does
// not let us set custom headers, so the query param is the only path that
// works in the browser.
export function connectEvents(
  onEvent: EventHandler,
  onClose?: () => void,
  onOpen?: () => void,
  onResync?: ResyncHandler,
  lastSeq = 0,
  onPing?: () => void,
): { close: () => void } {
  // The locale rides in the QUERY STRING and not in a header for the same
  // reason lastSeq does: EventSource cannot set one. The daemon accepts both
  // spellings (api.LocaleHeader / api.LocaleQueryParam) and this stream is the
  // reason the second exists — it carries the per-step messages of a
  // dependency migration, which the daemon renders per subscriber.
  const params = new URLSearchParams({ locale: currentLocale() })
  if (lastSeq > 0) params.set('lastSeq', String(lastSeq))
  const es = new EventSource(`/api/v1/events?${params.toString()}`)

  es.onopen = () => {
    onOpen?.()
  }

  es.onmessage = (msg) => {
    try {
      const event: EventDto = JSON.parse(msg.data)
      onEvent(event)
    } catch {
      // ignore malformed events
    }
  }

  es.addEventListener('resync', () => {
    onResync?.()
  })

  // Named keepalive from the daemon. Its arrival is the client's proof that the
  // SSE transport actually delivers incremental frames — on the Windows WebView2
  // asset server the stream is buffered and NO ping ever arrives, which the
  // store uses to switch to a polling fallback. Carries no payload of interest.
  es.addEventListener('ping', () => {
    onPing?.()
  })

  es.onerror = () => {
    es.close()
    onClose?.()
  }

  return { close: () => es.close() }
}
