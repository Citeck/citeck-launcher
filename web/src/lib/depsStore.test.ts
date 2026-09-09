import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useDepsStore, upgradeSetKey } from './depsStore'
import { getDependencies } from './api'

vi.mock('./api', () => ({ getDependencies: vi.fn() }))

const empty = { migration: null, result: null, dismissedKey: null, data: null, rollbackPending: '' }

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

  it('does not carry one migration\'s history into another', () => {
    const s = useDepsStore.getState()
    s.onStart('postgres', 10)
    s.onProgress({ appName: 'postgres', phase: 'dump', current: 4, total: 10, percent: 42, after: 'dumping' })
    s.onProgress({ appName: 'postgres', phase: 'restore', current: 8, total: 10, percent: 0, after: '' })
    s.hydrate({ id: 'rabbitmq', step: 'dump', stepIndex: 1, stepCount: 5 })
    const m = useDepsStore.getState().migration!
    expect(m.id).toBe('rabbitmq')
    expect(m.done).toEqual([])
    expect(m.messages).toEqual([])
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
    expect(useDepsStore.getState().rollbackPending).toBe('rollback pending')
  })

  // The pending-rollback alarm has TWO writers that report ONE daemon
  // condition in the same words: GET /namespace/dependencies, and the ordinary
  // namespace fetch (NamespaceDto.dependencyRollbackPending). Holding it in a
  // single field is what lets whichever of them ran last both raise it and
  // take it down — reading it off the dependencies payload alone left the
  // alarm waiting for a remount, a namespace switch or the banner's poll.
  it('keeps the pending rollback in one field, written by the deps GET and by the namespace fetch', async () => {
    vi.mocked(getDependencies).mockResolvedValueOnce({ items: [], rollbackPending: 'rollback pending' })
    await useDepsStore.getState().refresh()
    expect(useDepsStore.getState().rollbackPending).toBe('rollback pending')

    // A payload that no longer carries it takes the alarm down...
    vi.mocked(getDependencies).mockResolvedValueOnce({ items: [] })
    await useDepsStore.getState().refresh()
    expect(useDepsStore.getState().rollbackPending).toBe('')

    // ...and the namespace fetch writes the same field, in both directions.
    useDepsStore.getState().setRollbackPending('a previous migration of postgres left a rollback pending')
    expect(useDepsStore.getState().rollbackPending).toBe('a previous migration of postgres left a rollback pending')
    useDepsStore.getState().setRollbackPending('')
    expect(useDepsStore.getState().rollbackPending).toBe('')
  })

  // The banner and the dialog ask for the same payload on the same triggers (a
  // verdict, an open, a namespace switch), and they are mounted at the same
  // time — two surfaces must not become two requests. Callers join whatever is
  // already in flight; the joiner's answer can predate its own ask by at most
  // one round trip, which for an in-memory localhost route is nothing next to
  // doubling the request rate.
  it('serves callers that ask while a fetch is in flight from that one fetch', async () => {
    let release!: (dto: { items: [] }) => void
    vi.mocked(getDependencies).mockReturnValueOnce(new Promise((res) => { release = res }))
    const first = useDepsStore.getState().refresh()
    const second = useDepsStore.getState().refresh()
    expect(getDependencies).toHaveBeenCalledTimes(1)
    release({ items: [] })
    await Promise.all([first, second])
    expect(useDepsStore.getState().data).toEqual({ items: [] })

    // The dedupe lasts exactly as long as the request: the next ask is a new one.
    vi.mocked(getDependencies).mockResolvedValueOnce({ items: [] })
    await useDepsStore.getState().refresh()
    expect(getDependencies).toHaveBeenCalledTimes(2)
  })

  // A failed shared fetch must reject for every joiner, and must not leave the
  // store believing a request is still running (which would answer every later
  // refresh with a promise that can never settle).
  it('rejects every joiner of a failed fetch and still allows the next one', async () => {
    vi.mocked(getDependencies).mockRejectedValueOnce(new Error('nope'))
    const first = useDepsStore.getState().refresh()
    const second = useDepsStore.getState().refresh()
    await expect(first).rejects.toThrow('nope')
    await expect(second).rejects.toThrow('nope')
    expect(getDependencies).toHaveBeenCalledTimes(1)

    vi.mocked(getDependencies).mockResolvedValueOnce({ items: [] })
    await useDepsStore.getState().refresh()
    expect(getDependencies).toHaveBeenCalledTimes(2)
  })
  // A namespace has ONE progress channel and ONE result slot, shared by the
  // migration and the rollback. The SSE events carry no discriminator at all,
  // so the kind has to survive from the start of the run to its verdict — or
  // "postgres 18.6 → 17.5 succeeded" is indistinguishable from a migration
  // onto an older version.
  it('carries the rollback kind from the start through to the verdict', () => {
    const s = useDepsStore.getState()
    s.onStart('postgres', 3, 'rollback')
    s.onProgress({ appName: 'postgres', phase: 'switch-generation', current: 2, total: 3, percent: 0, after: '' })
    expect(useDepsStore.getState().migration!.kind).toBe('rollback')
    s.onComplete('postgres', 'rolled back')
    expect(useDepsStore.getState().result).toMatchObject({ kind: 'rollback', success: true })

    // A migration is the default, and it must not inherit the last kind.
    s.onStart('postgres', 10)
    expect(useDepsStore.getState().migration!.kind).toBe('')
    s.onError('postgres', 'boom')
    expect(useDepsStore.getState().result).toMatchObject({ kind: '', success: false })
  })

  // A rollback started elsewhere (the CLI, another window) reaches this client
  // as an ordinary `deps_migration_start` with no kind on it. The namespace
  // DTO is what says which it is, so hydrate has to teach the view.
  it('learns the kind from the namespace DTO when the events did not carry it', () => {
    const s = useDepsStore.getState()
    s.onStart('postgres', 3)
    expect(useDepsStore.getState().migration!.kind).toBe('')
    s.hydrate({ id: 'postgres', step: 'switch-generation', stepIndex: 2, stepCount: 3, kind: 'rollback' })
    expect(useDepsStore.getState().migration!.kind).toBe('rollback')

    // ...and on the path that deliberately KEEPS the live view: the DTO
    // describes a step SSE has already reported, so every other field of it is
    // refused as stale (it would drag percent and message backwards on each
    // refetch). The kind is the exception, because the events never carry it
    // at all — refusing it there is refusing the only source there is.
    s.onStart('rabbitmq', 3)
    s.onProgress({ appName: 'rabbitmq', phase: 'switch-generation', current: 2, total: 3, percent: 55, after: 'x' })
    s.hydrate({ id: 'rabbitmq', step: 'switch-generation', stepIndex: 2, stepCount: 3, percent: 0, kind: 'rollback' })
    const m = useDepsStore.getState().migration!
    expect(m.kind).toBe('rollback')
    expect(m.percent).toBe(55)
  })
})
