import type { EventDto } from './types'

export type EventHandler = (event: EventDto) => void

export function connectEvents(onEvent: EventHandler, onClose?: () => void): WebSocket {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const ws = new WebSocket(`${protocol}//${window.location.host}/api/v1/events`)

  ws.onmessage = (msg) => {
    try {
      const event: EventDto = JSON.parse(msg.data)
      onEvent(event)
    } catch {
      // ignore malformed events
    }
  }

  ws.onclose = () => {
    onClose?.()
  }

  return ws
}
