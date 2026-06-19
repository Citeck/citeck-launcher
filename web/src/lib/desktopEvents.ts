import type { EventDto } from './types'

// Desktop event transport. In the Wails webview the daemon's event stream is
// bridged to native Wails events by the wrapper (cmd/citeck-desktop/eventbridge
// .go) — this avoids the Windows WebView2 asset server buffering an in-page SSE
// EventSource (which froze statuses until a manual GET). Wails delivers backend
// events by calling window._wails.dispatchWailsEvent(evt); we intercept that to
// route the daemon:* events to store subscribers, delegating everything else to
// the runtime's own dispatcher.

const DAEMON_EVENT = 'daemon:event'
const DAEMON_PING = 'daemon:ping'
const DAEMON_RESYNC = 'daemon:resync'
const DAEMON_DISCONNECT = 'daemon:disconnect'

type WailsGlobal = { _wails?: Record<string, unknown> }

// Captured ONCE at module load — before installBridge() can create window._wails
// itself — so desktop detection reflects the genuine Wails runtime (which injects
// before the app bundle) and is never confused by our own _wails mutation.
const IN_WAILS_WEBVIEW = typeof window !== 'undefined' && !!(window as WailsGlobal)._wails

/** True inside the Wails desktop webview (the runtime sets window._wails). */
export function isWailsDesktop(): boolean {
  return IN_WAILS_WEBVIEW
}

interface DesktopSubscriber {
  onEvent: (e: EventDto) => void
  onResync: () => void
  onPing: () => void
  onDisconnect: () => void
}

const subscribers = new Set<DesktopSubscriber>()
let bridgeInstalled = false

type WailsEvent = { name?: string; data?: unknown }
type DispatchFn = (evt: WailsEvent) => void

function dispatchToSubscribers(name: string, data: unknown) {
  for (const s of subscribers) {
    try {
      if (name === DAEMON_EVENT) s.onEvent(data as EventDto)
      else if (name === DAEMON_RESYNC) s.onResync()
      else if (name === DAEMON_PING) s.onPing()
      else if (name === DAEMON_DISCONNECT) s.onDisconnect()
    } catch {
      // a faulty subscriber must not break delivery to the others
    }
  }
}

// installBridge wraps window._wails.dispatchWailsEvent with an interceptor. The
// Wails runtime may assign its real dispatcher BEFORE or AFTER this runs, so we
// install an accessor: the getter always returns our interceptor; the setter
// captures whatever the runtime later assigns as the delegate. Order-independent
// — Go's call to window._wails.dispatchWailsEvent(evt) always hits us, and the
// runtime's own (non-daemon) events still reach its dispatcher.
function installBridge() {
  if (bridgeInstalled) return
  bridgeInstalled = true
  const w = window as unknown as WailsGlobal
  w._wails = w._wails || {}

  let real: DispatchFn | undefined =
    typeof w._wails.dispatchWailsEvent === 'function' ? (w._wails.dispatchWailsEvent as DispatchFn) : undefined

  const interceptor: DispatchFn = (evt) => {
    const name = evt?.name
    if (typeof name === 'string' && name.startsWith('daemon:')) {
      dispatchToSubscribers(name, evt.data)
      return
    }
    real?.(evt)
  }

  try {
    Object.defineProperty(w._wails, 'dispatchWailsEvent', {
      configurable: true,
      get: () => interceptor,
      set: (fn: DispatchFn) => { real = fn },
    })
  } catch {
    // Property somehow non-configurable — fall back to a plain wrap (loses
    // delegation if the runtime reassigns afterwards, but daemon events work).
    w._wails.dispatchWailsEvent = interceptor as unknown as Record<string, unknown>['dispatchWailsEvent']
  }
}

/** Subscribe to the bridged daemon event stream. Returns an unsubscribe fn. */
export function connectDesktopEvents(
  onEvent: (e: EventDto) => void,
  onResync: () => void,
  onPing: () => void,
  onDisconnect: () => void,
): { close: () => void } {
  installBridge()
  const sub: DesktopSubscriber = { onEvent, onResync, onPing, onDisconnect }
  subscribers.add(sub)
  return { close: () => { subscribers.delete(sub) } }
}
