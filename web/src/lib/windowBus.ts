/**
 * Lightweight one-way bus between Wails secondary windows and the main
 * dashboard. SSE events emitted by the daemon reach both webviews in
 * principle, but a backgrounded Wails window can throttle EventSource
 * dispatch — so an edit applied via WindowEditor / WindowFileEditor can
 * sit in the dashboard's event queue until something else wakes it.
 *
 * BroadcastChannel is same-origin and synchronous: when the editor calls
 * `publishRefresh`, the dashboard's `subscribeRefresh` handler runs on
 * the next microtask and can fire fetchData immediately, without waiting
 * for an SSE chunk to be flushed.
 *
 * No data payload is sent — the dashboard already knows how to refetch.
 * The signal is just "something you might care about just changed".
 */

const CHANNEL = 'citeck-launcher-refresh'

type Listener = () => void

let channel: BroadcastChannel | null = null

function ensureChannel(): BroadcastChannel | null {
  if (typeof BroadcastChannel === 'undefined') return null
  if (channel) return channel
  channel = new BroadcastChannel(CHANNEL)
  return channel
}

export function publishRefresh(): void {
  const ch = ensureChannel()
  if (!ch) return
  ch.postMessage({ kind: 'refresh', ts: Date.now() })
}

export function subscribeRefresh(listener: Listener): () => void {
  const ch = ensureChannel()
  if (!ch) return () => { /* no-op */ }
  const handler = (e: MessageEvent) => {
    if (e.data && typeof e.data === 'object' && e.data.kind === 'refresh') listener()
  }
  ch.addEventListener('message', handler)
  return () => ch?.removeEventListener('message', handler)
}
