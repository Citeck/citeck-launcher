import type { EventDto } from './types'

export type EventHandler = (event: EventDto) => void

// SSE-based event stream (Server-Sent Events, no external deps)
export function connectEvents(
  onEvent: EventHandler,
  onClose?: () => void,
  onOpen?: () => void,
): { close: () => void } {
  const url = `/api/v1/events`
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

  es.onerror = () => {
    es.close()
    onClose?.()
  }

  return { close: () => es.close() }
}
