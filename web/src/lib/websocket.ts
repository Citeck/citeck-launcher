import type { EventDto } from './types'

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
  const url = lastSeq > 0 ? `/api/v1/events?lastSeq=${lastSeq}` : `/api/v1/events`
  const es = new EventSource(url)

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
