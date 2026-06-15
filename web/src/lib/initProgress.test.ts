import { describe, it, expect } from 'vitest'
import { initProgressOf } from './initProgress'

const app = (over: Partial<Parameters<typeof initProgressOf>[0]> = {}) => ({
  status: 'STARTING',
  initStep: 2,
  initTotal: 5,
  initName: 'ecos-app-x',
  ...over,
})

describe('initProgressOf', () => {
  it('returns step/total/name while STARTING with init fields present', () => {
    expect(initProgressOf(app())).toEqual({ step: 2, total: 5, name: 'ecos-app-x' })
  })

  it('returns null when the app is not STARTING (stale SSE patch guard)', () => {
    for (const status of ['RUNNING', 'PULLING', 'STOPPED', 'START_FAILED', 'UPDATING']) {
      expect(initProgressOf(app({ status }))).toBeNull()
    }
  })

  it('returns null when init fields are absent or cleared (phase done)', () => {
    expect(initProgressOf(app({ initStep: undefined, initTotal: undefined, initName: undefined }))).toBeNull()
    // Phase-done app_init_step event zeroes the fields.
    expect(initProgressOf(app({ initStep: 0, initTotal: 0, initName: undefined }))).toBeNull()
    // Defensive: partial data (no total) is treated as "no init phase".
    expect(initProgressOf(app({ initTotal: undefined }))).toBeNull()
    expect(initProgressOf(app({ initStep: undefined }))).toBeNull()
  })

  it('falls back to an empty name when initName is missing', () => {
    expect(initProgressOf(app({ initName: undefined }))).toEqual({ step: 2, total: 5, name: '' })
  })
})
