import { describe, it, expect } from 'vitest'
import { deriveStartProgress, STAGE_ORDER, type StageApp, type StartProgressInput } from './startProgress'

const app = (name: string, status: string, kind = 'CITECK_CORE'): StageApp => ({ name, status, kind })
const infra = (name: string, status: string): StageApp => ({ name, status, kind: 'THIRD_PARTY' })

const input = (over: Partial<StartProgressInput>): StartProgressInput => ({
  creating: false,
  nsStatus: 'STARTING',
  apps: [],
  pullProgress: {},
  ...over,
})

const states = (m: ReturnType<typeof deriveStartProgress>) => m.stages.map((s) => s.state)
const stage = (m: ReturnType<typeof deriveStartProgress>, id: string) => {
  const s = m.stages.find((x) => x.id === id)
  if (!s) throw new Error(`stage ${id} missing`)
  return s
}

describe('deriveStartProgress', () => {
  it('keeps stage order stable', () => {
    expect(STAGE_ORDER).toEqual(['bundle', 'images', 'infra', 'apps', 'ready'])
    expect(deriveStartProgress(input({})).stages.map((s) => s.id)).toEqual([...STAGE_ORDER])
  })

  it('createNamespace in flight → bundle active, everything else pending', () => {
    const m = deriveStartProgress(input({ creating: true, nsStatus: null }))
    expect(states(m)).toEqual(['active', 'pending', 'pending', 'pending', 'pending'])
    expect(m.running).toBe(false)
    expect(m.failed).toBe(false)
  })

  it('no namespace yet (fetch pending) → bundle still active', () => {
    const m = deriveStartProgress(input({ creating: false, nsStatus: null, apps: [] }))
    expect(states(m)).toEqual(['active', 'pending', 'pending', 'pending', 'pending'])
  })

  it('apps pulling → images active with counts and live pull lines (highest percent first)', () => {
    const m = deriveStartProgress(
      input({
        apps: [
          infra('postgres', 'PULLING'),
          infra('rabbitmq', 'READY_TO_PULL'),
          app('emodel', 'PULLING'),
          app('gateway', 'READY_TO_START'),
        ],
        pullProgress: {
          postgres: { percent: 45, phase: 'Pulling: 120mb 45%' },
          emodel: { percent: 80, phase: 'Extracting' },
        },
      }),
    )
    expect(states(m)).toEqual(['done', 'active', 'pending', 'pending', 'pending'])
    const img = stage(m, 'images')
    expect(img.total).toBe(4)
    expect(img.done).toBe(1) // 4 active apps, 3 still in pull states
    expect(img.pulls).toEqual([
      { name: 'emodel', percent: 80, phase: 'Extracting' },
      { name: 'postgres', percent: 45, phase: 'Pulling: 120mb 45%' },
    ])
  })

  it('pulls finished, infra starting → infra active; STARTING app preferred as currentApp', () => {
    const m = deriveStartProgress(
      input({
        apps: [
          infra('postgres', 'RUNNING'),
          infra('keycloak', 'STARTING'),
          infra('zookeeper', 'DEPS_WAITING'),
          app('emodel', 'READY_TO_START'),
        ],
      }),
    )
    expect(states(m)).toEqual(['done', 'done', 'active', 'pending', 'pending'])
    const inf = stage(m, 'infra')
    expect(inf.done).toBe(1)
    expect(inf.total).toBe(3)
    expect(inf.currentApp).toEqual({ name: 'keycloak', status: 'STARTING' })
  })

  it('infra up, webapps starting → apps active with running counts', () => {
    const m = deriveStartProgress(
      input({
        apps: [
          infra('postgres', 'RUNNING'),
          app('emodel', 'RUNNING'),
          app('gateway', 'STARTING'),
          app('uiserv', 'DEPS_WAITING'),
        ],
      }),
    )
    expect(states(m)).toEqual(['done', 'done', 'done', 'active', 'pending'])
    const a = stage(m, 'apps')
    expect(a.done).toBe(1)
    expect(a.total).toBe(3)
    expect(a.currentApp).toEqual({ name: 'gateway', status: 'STARTING' })
  })

  it('all apps up but namespace not RUNNING yet → ready active', () => {
    const m = deriveStartProgress(
      input({ apps: [infra('postgres', 'RUNNING'), app('emodel', 'HEALTHY')] }),
    )
    expect(states(m)).toEqual(['done', 'done', 'done', 'done', 'active'])
  })

  it('namespace RUNNING → all stages done, running=true', () => {
    const m = deriveStartProgress(
      input({ nsStatus: 'RUNNING', apps: [infra('postgres', 'RUNNING'), app('emodel', 'STARTING')] }),
    )
    expect(states(m)).toEqual(['done', 'done', 'done', 'done', 'done'])
    expect(m.running).toBe(true)
    expect(m.failed).toBe(false)
  })

  it('PULL_FAILED → images stage error with the failed app', () => {
    const m = deriveStartProgress(
      input({ apps: [infra('postgres', 'PULL_FAILED'), app('emodel', 'READY_TO_PULL')] }),
    )
    expect(stage(m, 'images').state).toBe('error')
    expect(stage(m, 'images').failedApp).toEqual({ name: 'postgres', status: 'PULL_FAILED' })
    expect(m.failed).toBe(true)
  })

  it('infra START_FAILED → infra stage error; webapp START_FAILED → apps stage error', () => {
    const mi = deriveStartProgress(input({ apps: [infra('keycloak', 'START_FAILED')] }))
    expect(stage(mi, 'infra').state).toBe('error')
    expect(stage(mi, 'infra').failedApp).toEqual({ name: 'keycloak', status: 'START_FAILED' })

    const ma = deriveStartProgress(
      input({ apps: [infra('postgres', 'RUNNING'), app('emodel', 'START_FAILED')] }),
    )
    expect(stage(ma, 'apps').state).toBe('error')
    expect(stage(ma, 'apps').failedApp).toEqual({ name: 'emodel', status: 'START_FAILED' })
  })

  it('detached (STOPPED) apps are excluded from every count', () => {
    const m = deriveStartProgress(
      input({
        apps: [
          infra('postgres', 'RUNNING'),
          app('emodel', 'STARTING'),
          app('onlyoffice', 'STOPPED'), // detached — must not count
        ],
      }),
    )
    const a = stage(m, 'apps')
    expect(a.total).toBe(1)
    expect(a.done).toBe(0)
  })

  it('STALLED namespace without a specific failed app → active stage goes error', () => {
    const m = deriveStartProgress(
      input({ nsStatus: 'STALLED', apps: [infra('postgres', 'RUNNING'), app('emodel', 'STARTING')] }),
    )
    expect(stage(m, 'apps').state).toBe('error')
    expect(m.failed).toBe(true)
  })

  it('pull lines only include apps with live pullProgress entries', () => {
    const m = deriveStartProgress(
      input({
        apps: [infra('postgres', 'PULLING'), infra('mongo', 'PULLING')],
        pullProgress: { postgres: { percent: 10, phase: '' } },
      }),
    )
    expect(stage(m, 'images').pulls).toEqual([{ name: 'postgres', percent: 10, phase: '' }])
  })
})
