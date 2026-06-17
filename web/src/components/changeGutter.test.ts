import { describe, it, expect } from 'vitest'
import { diffLineKinds } from './changeGutter'

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
