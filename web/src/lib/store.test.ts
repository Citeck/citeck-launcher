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

import { getNamespace, getHealth } from './api'

const mockedGetNamespace = vi.mocked(getNamespace)
const mockedGetHealth = vi.mocked(getHealth)

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
})
