import { describe, it, expect } from 'vitest'
import { diffLineKinds, alignLines } from './changeGutter'

describe('diffLineKinds', () => {
  it('marks added and changed lines vs baseline', () => {
    expect(diffLineKinds('a: 1\nb: 2\n', 'a: 9\nb: 2\nc: 3\n')).toEqual(['changed', 'unchanged', 'added'])
  })
  it('all unchanged when identical', () => {
    expect(diffLineKinds('x\ny\n', 'x\ny\n')).toEqual(['unchanged', 'unchanged'])
  })
  it('marks every line added when baseline is empty', () => {
    expect(diffLineKinds('', 'a\nb\n')).toEqual(['added', 'added'])
  })
})

describe('alignLines', () => {
  it('maps changed lines to the baseline text to restore', () => {
    const ops = alignLines('a: 1\nb: 2\n', 'a: 9\nb: 2\nc: 3\n')
    expect(ops[0]).toEqual({ kind: 'changed', base: 'a: 1' }) // revert → 'a: 1'
    expect(ops[1]).toEqual({ kind: 'unchanged', base: 'b: 2' })
    expect(ops[2]).toEqual({ kind: 'added', base: null }) // revert → delete
  })
  it('treats inserted lines with no baseline counterpart as added', () => {
    const ops = alignLines('keep\n', 'NEW1\nkeep\nNEW2\n')
    expect(ops.map((o) => o.kind)).toEqual(['added', 'unchanged', 'added'])
  })
})
