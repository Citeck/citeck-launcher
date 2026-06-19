import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useDashboardStore } from './store'

// Mock the api module
vi.mock('./api', () => ({
  getNamespace: vi.fn(),
  getHealth: vi.fn(),
}))

// Mock the websocket module
vi.mock('./websocket', () => ({
  connectEvents: vi.fn(() => ({ close: vi.fn() })),
}))

// Mock the toast module
vi.mock('./toast', () => ({
  toast: vi.fn(),
}))

// Mock the desktop (Wails) event bridge — defaults to "not desktop" so the
// existing tests take the EventSource path; one test flips it to true.
vi.mock('./desktopEvents', () => ({
  isWailsDesktop: vi.fn(() => false),
  connectDesktopEvents: vi.fn(() => ({ close: vi.fn() })),
}))

import { getNamespace, getHealth } from './api'
import { connectEvents } from './websocket'
import { isWailsDesktop, connectDesktopEvents } from './desktopEvents'
import type { EventDto } from './types'

const mockedGetNamespace = vi.mocked(getNamespace)
const mockedGetHealth = vi.mocked(getHealth)
const mockedConnectEvents = vi.mocked(connectEvents)
const mockedIsWailsDesktop = vi.mocked(isWailsDesktop)
const mockedConnectDesktop = vi.mocked(connectDesktopEvents)

describe('useDashboardStore', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    // Reset store state before each test
    useDashboardStore.setState({
      namespace: null,
      health: null,
      loading: true,
      error: null,
      stream: null,
      reconnectDelay: 1000,
      lastSeq: 0,
      reconnectGen: 0,
      diskLow: null,
    })
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('fetchData sets namespace and health on success', async () => {
    const namespace = { id: 'ns1', name: 'Test', status: 'RUNNING', bundleRef: 'community:2025.12', apps: [] }
    const health = { status: 'healthy', healthy: true, checks: [] }

    mockedGetNamespace.mockResolvedValueOnce(namespace)
    mockedGetHealth.mockResolvedValueOnce(health)

    await useDashboardStore.getState().fetchData()

    const state = useDashboardStore.getState()
    expect(state.namespace).toEqual(namespace)
    expect(state.health).toEqual(health)
    expect(state.loading).toBe(false)
    expect(state.error).toBeNull()
  })

  it('fetchData sets error on non-503 failure', async () => {
    // First set namespace to non-null so it's not the initial load
    useDashboardStore.setState({ namespace: { id: 'ns1', name: 'x', status: 'RUNNING', bundleRef: '', apps: [] } })

    mockedGetNamespace.mockRejectedValueOnce(new Error('Network error'))
    mockedGetHealth.mockRejectedValueOnce(new Error('Network error'))

    await useDashboardStore.getState().fetchData()

    const state = useDashboardStore.getState()
    expect(state.error).toBe('Network error')
    expect(state.loading).toBe(false)
  })

  it('fetchData retries on 503 during initial load', async () => {
    // namespace is null => initial load
    expect(useDashboardStore.getState().namespace).toBeNull()

    // First call: 503
    mockedGetNamespace.mockRejectedValueOnce(new Error('503 Service Unavailable'))
    mockedGetHealth.mockRejectedValueOnce(new Error('503 Service Unavailable'))

    // Second call (retry): success
    const namespace = { id: 'ns1', name: 'Test', status: 'RUNNING', bundleRef: '', apps: [] }
    const health = { status: 'healthy', healthy: true, checks: [] }
    mockedGetNamespace.mockResolvedValueOnce(namespace)
    mockedGetHealth.mockResolvedValueOnce(health)

    // Trigger first fetch (will fail with 503)
    const fetchPromise = useDashboardStore.getState().fetchData()
    await fetchPromise

    // After 503 on initial load, fetchData schedules a retry via setTimeout(1000)
    // The error should NOT be set (silent retry)
    expect(useDashboardStore.getState().error).toBeNull()

    // Advance timers to trigger the retry
    await vi.advanceTimersByTimeAsync(1000)

    // Now the second call should have succeeded
    const state = useDashboardStore.getState()
    expect(state.namespace).toEqual(namespace)
    expect(state.loading).toBe(false)
  })

  it('fetchData retries on DAEMON_STARTING during initial load', async () => {
    mockedGetNamespace.mockRejectedValueOnce(new Error('DAEMON_STARTING'))
    mockedGetHealth.mockRejectedValueOnce(new Error('DAEMON_STARTING'))

    const namespace = { id: 'ns1', name: 'Test', status: 'STOPPED', bundleRef: '', apps: [] }
    const health = { status: 'healthy', healthy: true, checks: [] }
    mockedGetNamespace.mockResolvedValueOnce(namespace)
    mockedGetHealth.mockResolvedValueOnce(health)

    await useDashboardStore.getState().fetchData()

    // Should not show error during retry
    expect(useDashboardStore.getState().error).toBeNull()

    await vi.advanceTimersByTimeAsync(1000)

    expect(useDashboardStore.getState().namespace).toEqual(namespace)
  })

  it('fetchData retries on Failed to fetch during initial load', async () => {
    mockedGetNamespace.mockRejectedValueOnce(new Error('Failed to fetch'))
    mockedGetHealth.mockRejectedValueOnce(new Error('Failed to fetch'))

    const namespace = { id: 'ns1', name: 'OK', status: 'RUNNING', bundleRef: '', apps: [] }
    const health = { status: 'healthy', healthy: true, checks: [] }
    mockedGetNamespace.mockResolvedValueOnce(namespace)
    mockedGetHealth.mockResolvedValueOnce(health)

    await useDashboardStore.getState().fetchData()

    // Silent retry — no error shown
    expect(useDashboardStore.getState().error).toBeNull()

    await vi.advanceTimersByTimeAsync(1000)

    expect(useDashboardStore.getState().namespace).toEqual(namespace)
  })

  it('fetchData does NOT retry 503 when already loaded (not initial)', async () => {
    // Set existing namespace — not initial load
    useDashboardStore.setState({ namespace: { id: 'ns1', name: 'x', status: 'RUNNING', bundleRef: '', apps: [] } })

    mockedGetNamespace.mockRejectedValueOnce(new Error('503 Service Unavailable'))
    mockedGetHealth.mockRejectedValueOnce(new Error('503 Service Unavailable'))

    await useDashboardStore.getState().fetchData()

    // Should show error immediately (no retry for non-initial load)
    const state = useDashboardStore.getState()
    expect(state.error).toBe('503 Service Unavailable')
  })

  it('fetchData clears a stale namespace when the daemon reports no namespace configured', async () => {
    // An active namespace that has since been deactivated (back-to-welcome).
    useDashboardStore.setState({ namespace: { id: 'ns1', name: 'x', status: 'STOPPED', bundleRef: '', apps: [] } })

    mockedGetNamespace.mockRejectedValueOnce(new Error('no namespace configured'))
    mockedGetHealth.mockRejectedValueOnce(new Error('no namespace configured'))

    await useDashboardStore.getState().fetchData()

    // Stale namespace must be cleared (so Welcome + workspace picker show), no error.
    const state = useDashboardStore.getState()
    expect(state.namespace).toBeNull()
    expect(state.error).toBeNull()
    expect(state.loading).toBe(false)
  })

  it('disk_low sets diskLow, dismissal clears it, a new event re-shows it, disk_ok clears it', () => {
    useDashboardStore.getState().startEventStream()
    expect(mockedConnectEvents).toHaveBeenCalledTimes(1)
    const onEvent = mockedConnectEvents.mock.calls[0][0]

    const base: EventDto = { type: '', seq: 0, timestamp: 0, namespaceId: '', appName: '', before: '', after: '' }

    // Trip: disk_low carries path + freeBytes + thresholdBytes
    onEvent({ ...base, type: 'disk_low', seq: 1, path: '/var/lib/citeck', freeBytes: 3.2 * 2 ** 30, thresholdBytes: 5 * 2 ** 30 })
    expect(useDashboardStore.getState().diskLow).toEqual({
      path: '/var/lib/citeck',
      freeBytes: 3.2 * 2 ** 30,
      thresholdBytes: 5 * 2 ** 30,
    })

    // User dismisses the banner — state clears without a daemon event
    useDashboardStore.getState().dismissDiskLow()
    expect(useDashboardStore.getState().diskLow).toBeNull()

    // A NEW disk_low event (the daemon emits on state change only, so this
    // means recovery + a fresh trip) re-shows the banner after dismissal
    onEvent({ ...base, type: 'disk_low', seq: 2, path: '/var/lib/citeck', freeBytes: 2 ** 30, thresholdBytes: 5 * 2 ** 30 })
    expect(useDashboardStore.getState().diskLow?.freeBytes).toBe(2 ** 30)

    // Recovery clears it
    onEvent({ ...base, type: 'disk_ok', seq: 3, path: '/var/lib/citeck', freeBytes: 50 * 2 ** 30 })
    expect(useDashboardStore.getState().diskLow).toBeNull()

    useDashboardStore.getState().stopEventStream()
  })

  it('polls fetchData as a fallback when the SSE stream delivers no frames (buffered transport)', async () => {
    const namespace = { id: 'ns1', name: 'Test', status: 'STARTING', bundleRef: '', apps: [] }
    const health = { status: 'healthy', healthy: true, checks: [] }
    mockedGetNamespace.mockResolvedValue(namespace)
    mockedGetHealth.mockResolvedValue(health)
    // Active namespace (poll only refreshes when one is selected).
    useDashboardStore.setState({ namespace })

    useDashboardStore.getState().startEventStream()
    // No onOpen / onEvent / onPing fired → the stream never delivered a frame,
    // so after SSE_STALE_MS the poll tick (3s) must call fetchData.
    expect(mockedGetNamespace).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(3000)
    expect(mockedGetNamespace).toHaveBeenCalled()

    useDashboardStore.getState().stopEventStream()
  })

  it('does NOT poll while the SSE stream delivers ping keepalives (healthy transport)', async () => {
    const namespace = { id: 'ns1', name: 'Test', status: 'STARTING', bundleRef: '', apps: [] }
    mockedGetNamespace.mockResolvedValue(namespace)
    mockedGetHealth.mockResolvedValue({ status: 'healthy', healthy: true, checks: [] })
    useDashboardStore.setState({ namespace })

    useDashboardStore.getState().startEventStream()
    const onPing = mockedConnectEvents.mock.calls.at(-1)![5]
    expect(onPing).toBeTypeOf('function')

    // Daemon pings every 10s; simulate 30s of healthy keepalives. Each ping
    // refreshes the freshness window so the 3s poll tick never trips.
    for (let i = 0; i < 3; i++) {
      onPing!()
      await vi.advanceTimersByTimeAsync(10_000)
    }
    expect(mockedGetNamespace).not.toHaveBeenCalled()

    useDashboardStore.getState().stopEventStream()
  })

  it('pull_auth_required records the host; leaving PULL_FAILED clears the marker', () => {
    useDashboardStore.setState({ pullAuthRequired: {} })
    useDashboardStore.getState().startEventStream()
    const onEvent = mockedConnectEvents.mock.calls[0][0]
    const base: EventDto = { type: '', seq: 0, timestamp: 0, namespaceId: '', appName: '', before: '', after: '' }

    // Pull auth failure carries the registry host in `after`.
    onEvent({ ...base, type: 'pull_auth_required', seq: 1, appName: 'emodel', after: 'harbor.citeck.ru' })
    expect(useDashboardStore.getState().pullAuthRequired).toEqual({ emodel: 'harbor.citeck.ru' })

    // An event without a host is ignored (no empty marker).
    onEvent({ ...base, type: 'pull_auth_required', seq: 2, appName: 'gateway', after: '' })
    expect(useDashboardStore.getState().pullAuthRequired).toEqual({ emodel: 'harbor.citeck.ru' })

    // Leaving PULL_FAILED drops only that app's marker.
    onEvent({ ...base, type: 'app_status', seq: 3, appName: 'emodel', before: 'PULL_FAILED', after: 'PULLING' })
    expect(useDashboardStore.getState().pullAuthRequired).toEqual({})

    useDashboardStore.getState().stopEventStream()
  })

  it('uses the Wails bridge transport in desktop mode (not EventSource)', () => {
    mockedIsWailsDesktop.mockReturnValue(true)
    try {
      useDashboardStore.getState().startEventStream()
      expect(mockedConnectDesktop).toHaveBeenCalledTimes(1)
      expect(mockedConnectEvents).not.toHaveBeenCalled()

      // The first bridge arg is the shared event handler — a daemon event
      // routed through it updates the store exactly like an SSE frame would.
      const onEvent = mockedConnectDesktop.mock.calls[0][0]
      const base: EventDto = { type: '', seq: 0, timestamp: 0, namespaceId: '', appName: '', before: '', after: '' }
      onEvent({ ...base, type: 'disk_low', seq: 1, path: '/x', freeBytes: 1, thresholdBytes: 2 })
      expect(useDashboardStore.getState().diskLow).toEqual({ path: '/x', freeBytes: 1, thresholdBytes: 2 })

      useDashboardStore.getState().stopEventStream()
    } finally {
      mockedIsWailsDesktop.mockReturnValue(false)
    }
  })
})
