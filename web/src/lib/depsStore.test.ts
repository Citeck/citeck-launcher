import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useDepsStore, upgradeSetKey } from './depsStore'
import { getDependencies } from './api'

vi.mock('./api', () => ({ getDependencies: vi.fn() }))

const empty = { migration: null, result: null, dismissedKey: null, data: null }

beforeEach(() => {
  useDepsStore.setState(empty)
  vi.mocked(getDependencies).mockReset()
})

describe('depsStore', () => {
  it('tracks completed steps from progress events', () => {
    const s = useDepsStore.getState()
    s.onStart('postgres', 10)
    s.onProgress({ appName: 'postgres', phase: 'stop-namespace', current: 1, total: 10, percent: 0, after: '' })
    s.onProgress({ appName: 'postgres', phase: 'pull-image', current: 2, total: 10, percent: 30, after: 'pulling' })
    const m = useDepsStore.getState().migration!
    expect(m.step).toBe('pull-image')
    expect(m.done).toEqual(['stop-namespace'])
    expect(m.percent).toBe(30)
    expect(m.stepIndex).toBe(2)
    expect(m.stepCount).toBe(10)
  })

  it('keeps the last messages, newest last, without repeating one', () => {
    const s = useDepsStore.getState()
    s.onStart('postgres', 10)
    for (const [phase, after] of [
      ['dump', 'dumped 1 GiB'],
      ['dump', 'dumped 1 GiB'],
      ['dump', 'dumped 2 GiB'],
      ['restore', ''],
    ] as const) {
      s.onProgress({ appName: 'postgres', phase, current: 4, total: 10, percent: 0, after })
    }
    expect(useDepsStore.getState().migration!.messages).toEqual(['dumped 1 GiB', 'dumped 2 GiB'])
  })

  it('ends with a result on complete and on error', () => {
    const s = useDepsStore.getState()
    s.onStart('postgres', 2)
    s.onError('postgres', 'boom')
    expect(useDepsStore.getState().migration).toBeNull()
    expect(useDepsStore.getState().result).toMatchObject({ id: 'postgres', success: false, message: 'boom' })
    s.onStart('postgres', 2)
    expect(useDepsStore.getState().result).toBeNull()
    s.onComplete('postgres', 'done')
    expect(useDepsStore.getState().result).toMatchObject({ success: true })
  })

  // The POST that starts a migration answers only after the daemon has built
  // the plan, and the daemon broadcasts `deps_migration_start` before that —
  // so the optimistic start the dialog fires on a 202 can land AFTER real
  // progress has already been recorded. It must not wipe it.
  it('a repeated start for the same migration keeps the progress already seen', () => {
    const s = useDepsStore.getState()
    s.onStart('postgres', 10)
    s.onProgress({ appName: 'postgres', phase: 'dump', current: 4, total: 10, percent: 42, after: 'dumping' })
    s.onStart('postgres', 0)
    const m = useDepsStore.getState().migration!
    expect(m.step).toBe('dump')
    expect(m.percent).toBe(42)
    expect(m.stepCount).toBe(10)
  })

  it('hydrates from the namespace DTO after a reconnect without losing done steps', () => {
    const s = useDepsStore.getState()
    s.hydrate({ id: 'postgres', step: 'dump', stepIndex: 4, stepCount: 10 })
    expect(useDepsStore.getState().migration).toMatchObject({ step: 'dump', stepIndex: 4, stepCount: 10 })
    s.onProgress({ appName: 'postgres', phase: 'stop-source', current: 5, total: 10, percent: 0, after: '' })
    expect(useDepsStore.getState().migration!.done).toEqual(['dump'])
    s.hydrate(null)
    expect(useDepsStore.getState().migration).toBeNull()
  })

  // The DTO is a snapshot taken when the fetch was answered; SSE is live. A
  // hydrate that overwrote a step the events already moved past would drag the
  // progress list backwards on every refetch.
  it('does not drag the view back to a step SSE already left', () => {
    const s = useDepsStore.getState()
    s.onStart('postgres', 10)
    s.onProgress({ appName: 'postgres', phase: 'dump', current: 4, total: 10, percent: 42, after: 'dumping' })
    s.hydrate({ id: 'postgres', step: 'dump', stepIndex: 4, stepCount: 10, percent: 0 })
    expect(useDepsStore.getState().migration!.percent).toBe(42)
  })

  it('builds a stable key for the upgrade set', () => {
    expect(upgradeSetKey(undefined)).toBe('')
    expect(upgradeSetKey([{ id: 'postgres', app: 'postgres', from: 'a', to: 'b', migratable: true }])).toBe('postgres:b')
  })

  it('refresh publishes the dependencies payload and rethrows a failure', async () => {
    const dto = { items: [], rollbackPending: 'rollback pending' }
    vi.mocked(getDependencies).mockResolvedValueOnce(dto)
    await useDepsStore.getState().refresh()
    expect(useDepsStore.getState().data).toEqual(dto)

    vi.mocked(getDependencies).mockRejectedValueOnce(new Error('nope'))
    await expect(useDepsStore.getState().refresh()).rejects.toThrow('nope')
    // The last good payload survives a failed refresh — a transient error must
    // not blank a rollback notice the user still has to act on.
    expect(useDepsStore.getState().data).toEqual(dto)
  })
})
