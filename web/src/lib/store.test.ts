import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useDashboardStore } from './store'
import { useDepsStore } from './depsStore'
import { useErrorModalStore } from './errorModal'

// Mock the api module
vi.mock('./api', async () => ({
  getNamespace: vi.fn(),
  getHealth: vi.fn(),
  getSnapshotOperation: vi.fn(),
  ApiError: (await vi.importActual<typeof import('./api')>('./api')).ApiError,
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

import { getNamespace, getHealth, getSnapshotOperation, ApiError } from './api'
import { useLongOpStore } from './longOp'
import { connectEvents } from './websocket'
import { isWailsDesktop, connectDesktopEvents } from './desktopEvents'
import type { EventDto } from './types'
import { toast } from './toast'

const mockedGetNamespace = vi.mocked(getNamespace)
const mockedGetHealth = vi.mocked(getHealth)
const mockedConnectEvents = vi.mocked(connectEvents)
const mockedIsWailsDesktop = vi.mocked(isWailsDesktop)
const mockedConnectDesktop = vi.mocked(connectDesktopEvents)

describe('useDashboardStore', () => {
  beforeEach(() => {
    useDashboardStore.getState().stopEventStream()
    useDepsStore.setState(useDepsStore.getInitialState())
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
    // No onOpen / onEvent / onPing fired → the stream is never proven live, so
    // phase 1 polls on the very first 3s tick (independent of SSE_STALE_MS).
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

  // The content storage connection state (NamespaceDto.contentStorage) is
  // rendered per reader by the daemon, so its SSE event carries no payload
  // worth patching in place: the store answers it with a namespace refetch,
  // which is what takes the banner from "connecting" to gone.
  it('refetches the namespace on a namespace_content_storage event', async () => {
    mockedGetNamespace.mockResolvedValue({ id: 'ns1', name: 'x', status: 'RUNNING', bundleRef: '', apps: [] })
    mockedGetHealth.mockResolvedValue({ status: 'healthy', healthy: true, checks: [] })
    useDashboardStore.getState().startEventStream()
    const onEvent = mockedConnectEvents.mock.calls[0][0]
    const base: EventDto = { type: '', seq: 0, timestamp: 0, namespaceId: 'ns1', appName: '', before: '', after: '' }

    onEvent({ ...base, type: 'namespace_content_storage', seq: 1, before: 'waiting', after: 'connected' })
    expect(mockedGetNamespace).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(150)
    expect(mockedGetNamespace).toHaveBeenCalledTimes(1)

    useDashboardStore.getState().stopEventStream()
  })

  // The dependency-migration lifecycle is owned by depsStore; the dashboard
  // store is only its SSE transport. Without this routing the Dependencies
  // dialog would show a migration that never moves.
  it('routes deps_migration_* events into the deps store', () => {
    useDepsStore.setState({ migration: null, result: null })
    useDashboardStore.getState().startEventStream()
    const onEvent = mockedConnectEvents.mock.calls[0][0]
    const base: EventDto = { type: '', seq: 0, timestamp: 0, namespaceId: 'ns1', appName: 'postgres', before: '', after: '' }

    onEvent({ ...base, type: 'deps_migration_start', seq: 1, total: 10, after: 'postgres:17.5 → postgres:18' })
    expect(useDepsStore.getState().migration).toMatchObject({ id: 'postgres', stepCount: 10 })

    onEvent({ ...base, type: 'deps_migration_progress', seq: 2, phase: 'dump', current: 4, total: 10, percent: 42, after: 'dumping' })
    expect(useDepsStore.getState().migration).toMatchObject({ step: 'dump', stepIndex: 4, percent: 42, message: 'dumping' })

    onEvent({ ...base, type: 'deps_migration_error', seq: 3, after: 'restore failed' })
    expect(useDepsStore.getState().migration).toBeNull()
    expect(useDepsStore.getState().result).toMatchObject({ id: 'postgres', success: false, message: 'restore failed' })

    onEvent({ ...base, type: 'deps_migration_start', seq: 4, total: 10 })
    onEvent({ ...base, type: 'deps_migration_complete', seq: 5, current: 10, total: 10, after: 'postgres migrated to postgres:18' })
    expect(useDepsStore.getState().result).toMatchObject({ success: true, message: 'postgres migrated to postgres:18' })

    useDashboardStore.getState().stopEventStream()
  })

  it('does not resurrect a completed migration from a pre-terminal namespace response', async () => {
    const stale = { id: 'ns1', name: 'x', status: 'STOPPED', bundleRef: '', apps: [],
      dependencyMigration: { id: 'rabbitmq', step: 'stop-new', stepIndex: 11, stepCount: 11 } }
    useDepsStore.getState().onStart('rabbitmq', 11)
    let releaseHealth!: (value: { status: string; healthy: boolean; checks: [] }) => void
    mockedGetNamespace.mockResolvedValueOnce(stale).mockResolvedValue({ ...stale, dependencyMigration: undefined })
    mockedGetHealth.mockReturnValueOnce(new Promise((resolve) => { releaseHealth = resolve }))
    const pending = useDashboardStore.getState().fetchData()
    mockedGetHealth.mockResolvedValue({ status: 'healthy', healthy: true, checks: [] })
    useDashboardStore.getState().startEventStream()
    const onEvent = mockedConnectEvents.mock.calls[0][0]
    onEvent({ type: 'deps_migration_complete', seq: 1, timestamp: 0,
      namespaceId: 'ns1', appName: 'rabbitmq', before: '', after: 'migrated' })
    releaseHealth({ status: 'healthy', healthy: true, checks: [] })
    await pending
    expect(useDepsStore.getState().migration).toBeNull()
    expect(useDepsStore.getState().result).toMatchObject({ id: 'rabbitmq', success: true })
    expect(useDashboardStore.getState().namespace?.dependencyMigration).toBeUndefined()
  })

  it('reconciles migration completion even while SSE keeps delivering pings', async () => {
    const ns = { id: 'ns1', name: 'x', status: 'STOPPED', bundleRef: '', apps: [] }
    useDashboardStore.setState({ namespace: ns })
    useDepsStore.getState().onStart('rabbitmq', 11)
    mockedGetNamespace.mockResolvedValue(ns)
    mockedGetHealth.mockResolvedValue({ status: 'healthy', healthy: true, checks: [] })
    useDashboardStore.getState().startEventStream()
    const onPing = mockedConnectEvents.mock.calls[0][5]
    onPing?.()
    await vi.advanceTimersByTimeAsync(3000)
    expect(mockedGetNamespace).toHaveBeenCalled()
    expect(useDepsStore.getState().migration).toBeNull()
  })

  it('silently coalesces sequence-gap resyncs without claiming connection recovery', async () => {
    mockedGetNamespace.mockResolvedValue({ id: 'ns1', name: 'x', status: 'STARTING', bundleRef: '', apps: [] })
    mockedGetHealth.mockResolvedValue({ status: 'healthy', healthy: true, checks: [] })
    useDashboardStore.getState().startEventStream()
    const onEvent = mockedConnectEvents.mock.calls[0][0]
    const base = { type: 'app_stats', timestamp: 0, namespaceId: 'ns1', appName: 'emodel', before: '', after: '' }
    for (const seq of [1, 3, 5, 7, 9, 11]) onEvent({ ...base, seq })
    expect(toast).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(100)
    expect(mockedGetNamespace).toHaveBeenCalledTimes(1)
  })

  it('never moves the replay cursor backwards on a late event', () => {
    useDashboardStore.getState().startEventStream()
    const onEvent = mockedConnectEvents.mock.calls[0][0]
    const base = { type: 'app_stats', timestamp: 0, namespaceId: 'ns1', appName: 'emodel', before: '', after: '' }
    for (const seq of [1, 3, 2]) onEvent({ ...base, seq })
    expect(useDashboardStore.getState().lastSeq).toBe(3)
  })

  // A client that connects or reloads mid-migration never saw the start event;
  // NamespaceDto.dependencyMigration is the namespace-scoped truth for it.
  it('hydrates the deps store from the namespace DTO, and clears it when the namespace goes away', async () => {
    const namespace = {
      id: 'ns1', name: 'x', status: 'STOPPED', bundleRef: '', apps: [],
      dependencyMigration: { id: 'postgres', step: 'dump', stepIndex: 4, stepCount: 10 },
    }
    mockedGetNamespace.mockResolvedValueOnce(namespace)
    mockedGetHealth.mockResolvedValueOnce({ status: 'healthy', healthy: true, checks: [] })
    await useDashboardStore.getState().fetchData()
    expect(useDepsStore.getState().migration).toMatchObject({ id: 'postgres', step: 'dump' })

    // A namespace with no migration clears it...
    mockedGetNamespace.mockResolvedValueOnce({ ...namespace, dependencyMigration: undefined })
    mockedGetHealth.mockResolvedValueOnce({ status: 'healthy', healthy: true, checks: [] })
    await useDashboardStore.getState().fetchData()
    expect(useDepsStore.getState().migration).toBeNull()

    // ...and so does a deactivated/deleted namespace, which never reaches the
    // success path at all — otherwise a stale migration view survives the
    // switch and blocks the controls of a namespace it has nothing to do with.
    useDepsStore.getState().hydrate({ id: 'postgres', step: 'dump', stepIndex: 4, stepCount: 10 })
    mockedGetNamespace.mockRejectedValueOnce(new Error('no namespace configured'))
    mockedGetHealth.mockRejectedValueOnce(new Error('no namespace configured'))
    await useDashboardStore.getState().fetchData()
    expect(useDepsStore.getState().migration).toBeNull()
  })

  // An interrupted migration whose restart-recovery ROLLBACK FAILED is
  // announced by nothing: no deps_migration_* event, no result. Before the
  // daemon carried it in NamespaceDto the alarm reached the user only when
  // something happened to re-read GET /namespace/dependencies — a remount, a
  // namespace switch, or the banner's own poll, which only runs once the alarm
  // is already up. The ordinary namespace fetch now raises it and clears it.
  it('publishes the namespace DTO pending rollback into the deps store, and clears it with the namespace', async () => {
    const pending = 'a previous migration of postgres left a rollback pending'
    const namespace = { id: 'ns1', name: 'x', status: 'STOPPED', bundleRef: '', apps: [], dependencyRollbackPending: pending }
    mockedGetNamespace.mockResolvedValueOnce(namespace)
    mockedGetHealth.mockResolvedValueOnce({ status: 'healthy', healthy: true, checks: [] })
    await useDashboardStore.getState().fetchData()
    expect(useDepsStore.getState().rollbackPending).toBe(pending)

    // The daemon's next start retried the rollback and it succeeded: the field
    // is simply absent, and the alarm has to come down on the same fetch.
    mockedGetNamespace.mockResolvedValueOnce({ ...namespace, dependencyRollbackPending: undefined })
    mockedGetHealth.mockResolvedValueOnce({ status: 'healthy', healthy: true, checks: [] })
    await useDashboardStore.getState().fetchData()
    expect(useDepsStore.getState().rollbackPending).toBe('')

    // And a namespace that went away takes its alarm with it — the same rule
    // as the migration view: it belongs to a namespace that is no longer here.
    useDepsStore.getState().setRollbackPending(pending)
    mockedGetNamespace.mockRejectedValueOnce(new Error('no namespace configured'))
    mockedGetHealth.mockRejectedValueOnce(new Error('no namespace configured'))
    await useDashboardStore.getState().fetchData()
    expect(useDepsStore.getState().rollbackPending).toBe('')
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

describe('snapshot byte progress transport', () => {
 it('starts on download events, keeps byte totals and clears on completion', async () => {
  const { useLongOpStore } = await import('./longOp')
  useDashboardStore.getState().stopEventStream()
  useLongOpStore.getState().end()
  vi.mocked(connectEvents).mockClear()
  useDashboardStore.getState().startEventStream()
  const onEvent = vi.mocked(connectEvents).mock.calls.at(-1)![0]
  const event = { seq: 0, timestamp: Date.now(), namespaceId: 'ns1', appName: '', before: '', after: '' }
  onEvent({ ...event, type: 'snapshot_download' })
  expect(useLongOpStore.getState().current?.kind).toBe('snapshot.download')
  onEvent({ ...event, type: 'snapshot_progress', phase: 'downloading', receivedBytes: 100000, totalBytes: 400000 })
  expect(useLongOpStore.getState().current?.progress).toMatchObject({ receivedBytes: 100000, totalBytes: 400000, phase: 'downloading' })
  onEvent({ ...event, type: 'snapshot_import' })
  expect(useLongOpStore.getState().current?.progress?.receivedBytes).toBeUndefined()
  onEvent({ ...event, type: 'snapshot_complete' })
  expect(useLongOpStore.getState().current).toBeNull()
  useDashboardStore.getState().stopEventStream()
 })
})


describe('snapshot status reconciliation', () => {
  afterEach(() => { useLongOpStore.getState().end(); useErrorModalStore.getState().close(); vi.useRealTimers() })
  const namespace = { id: 'ns1', name: 'Test', status: 'STOPPED', bundleRef: '', apps: [] }
  const event = { seq: 42, timestamp: 0, namespaceId: 'ns1', appName: '', before: '', after: '', type: 'snapshot_complete', operationId: 'accepted-op' }

  it('recovers a completion missing from the SSE ring and deduplicates its later replay', async () => {
    useLongOpStore.getState().start('snapshot.import', 'Import')
    useLongOpStore.getState().bindOperation('accepted-op')
    vi.mocked(getSnapshotOperation).mockResolvedValue(event)
    mockedGetNamespace.mockResolvedValue(namespace)
    mockedGetHealth.mockResolvedValue({ status: 'healthy', healthy: true, checks: [] })
    const before = useLongOpStore.getState().completed
    await useDashboardStore.getState().fetchData()
    expect(getSnapshotOperation).toHaveBeenLastCalledWith('accepted-op')
    expect(useLongOpStore.getState().current).toBeNull()
    expect(useLongOpStore.getState().completed).toBe(before + 1)
    const { applySnapshotEvent } = await import('./snapshotEvents')
    applySnapshotEvent(event)
    expect(useLongOpStore.getState().completed).toBe(before + 1)
  })

  it.each(['snapshot_download', 'snapshot_import'])('ignores replayed %s lifecycle after HTTP completion', async (startType) => {
    const { applySnapshotEvent, reconcileSnapshotOperation } = await import('./snapshotEvents')
    const id = `completed-replay-${startType}`
    const terminal = { ...event, operationId: id }
    useLongOpStore.getState().start('snapshot.import', 'Import')
    useLongOpStore.getState().bindOperation(id)
    vi.mocked(getSnapshotOperation).mockResolvedValue(terminal)
    const before = useLongOpStore.getState().completed
    await reconcileSnapshotOperation()
    expect(useLongOpStore.getState().current).toBeNull()
    // Another completion must not erase the first operation's replay protection.
    applySnapshotEvent({ ...terminal, operationId: `${id}-second` })
    const completed = useLongOpStore.getState().completed
    expect(completed).toBe(before + 2)
    for (const type of [startType, 'snapshot_progress', 'snapshot_complete']) {
      applySnapshotEvent({ ...terminal, type, current: 1, total: 2 })
      expect(useLongOpStore.getState().current).toBeNull()
      expect(useLongOpStore.getState().completed).toBe(completed)
    }
    useLongOpStore.getState().start('snapshot.import', 'New request')
    const pending = useLongOpStore.getState().current
    applySnapshotEvent({ ...terminal, type: 'snapshot_progress', current: 1, total: 2 })
    expect(useLongOpStore.getState().current).toEqual(pending)
  })

  it('does not clear a newer operation if an older status request finishes late', async () => {
    useLongOpStore.getState().start('snapshot.import', 'Old')
    useLongOpStore.getState().bindOperation('accepted-op')
    let resolve!: (e: EventDto) => void
    vi.mocked(getSnapshotOperation).mockImplementation(() => new Promise(r => { resolve = r }))
    mockedGetNamespace.mockResolvedValue(namespace)
    mockedGetHealth.mockResolvedValue({ status: 'healthy', healthy: true, checks: [] })
    const pending = useDashboardStore.getState().fetchData()
    await Promise.resolve(); await Promise.resolve()
    useLongOpStore.getState().start('snapshot.import', 'New')
    resolve(event)
    await pending
    expect(useLongOpStore.getState().current?.title).toBe('New')
  })

  it('does not turn repeated status reads into fresh progress', async () => {
    vi.useFakeTimers()
    useLongOpStore.getState().start('snapshot.import', 'Import')
    useLongOpStore.getState().bindOperation('pending-op')
    const before = useLongOpStore.getState().current!.lastProgressAt
    vi.setSystemTime(before + 10000)
    vi.mocked(getSnapshotOperation).mockResolvedValue({ ...event, operationId: 'pending-op', type: 'snapshot_progress', current: 1, total: 2 })
    const { reconcileSnapshotOperation } = await import('./snapshotEvents')
    await reconcileSnapshotOperation()
    expect(useLongOpStore.getState().current?.lastProgressAt).toBe(before)
  })

  it('reports unavailable status as unknown without claiming a completion', async () => {
    useLongOpStore.getState().start('snapshot.import', 'Import')
    useLongOpStore.getState().bindOperation('expired-op')
    vi.mocked(getSnapshotOperation).mockRejectedValue(new ApiError('completion cannot be confirmed', 404, 'STATUS_UNAVAILABLE'))
    mockedGetNamespace.mockResolvedValue(namespace)
    mockedGetHealth.mockResolvedValue({ status: 'healthy', healthy: true, checks: [] })
    const before = useLongOpStore.getState().completed
    await useDashboardStore.getState().fetchData()
    expect(useLongOpStore.getState().current).toBeNull()
    expect(useLongOpStore.getState().completed).toBe(before)
    expect(useErrorModalStore.getState().current?.message).toBe('completion cannot be confirmed')
  })
})


describe('snapshot status with a live event transport', () => {
  afterEach(() => {
    useDashboardStore.getState().stopEventStream()
    useLongOpStore.getState().end()
    mockedIsWailsDesktop.mockReturnValue(false)
    vi.useRealTimers()
  })
  it('polls the accepted result even when native pings continue', async () => {
    vi.useFakeTimers()
    mockedIsWailsDesktop.mockReturnValue(true)
    useDashboardStore.setState({ namespace: { id: 'ns1', name: '', status: 'STOPPED', bundleRef: '', apps: [] } })
    useLongOpStore.getState().start('snapshot.import', 'Import')
    useLongOpStore.getState().bindOperation('polled-op')
    vi.mocked(getSnapshotOperation).mockResolvedValue({ operationId: 'polled-op', type: 'snapshot_complete', seq: 1, timestamp: 0, namespaceId: 'ns1', appName: '', before: '', after: '' })
    useDashboardStore.getState().startEventStream()
    const [, , ping] = mockedConnectDesktop.mock.calls.at(-1)!
    ping()
    await vi.advanceTimersByTimeAsync(3000)
    expect(getSnapshotOperation).toHaveBeenLastCalledWith('polled-op')
    expect(useLongOpStore.getState().current).toBeNull()
    useDashboardStore.getState().stopEventStream()
    mockedIsWailsDesktop.mockReturnValue(false)
    vi.useRealTimers()
  })
})
