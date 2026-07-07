import { describe, it, expect } from 'vitest'
import { buildDragSelectionText, absOffsetInRow, resolveRowEndpoint, pickRowIndexAtY } from './logSelection'
import type { LogEntry } from '../hooks/useLogStream'

const E = (id: number, text: string): LogEntry => ({ id, text, level: null })
const ENTRIES: LogEntry[] = [E(10, 'alpha'), E(11, 'bravo'), E(12, 'charlie'), E(14, 'delta')]

describe('buildDragSelectionText (buffer-backed copy for virtualized selections)', () => {
  it('spans multiple rows with partial boundary lines', () => {
    expect(buildDragSelectionText(ENTRIES, { anchorId: 10, anchorOffset: 2, focusId: 12, focusOffset: 4 }))
      .toBe('pha\nbravo\nchar')
  })

  it('is direction-agnostic (upward drag gives the same text)', () => {
    expect(buildDragSelectionText(ENTRIES, { anchorId: 12, anchorOffset: 4, focusId: 10, focusOffset: 2 }))
      .toBe('pha\nbravo\nchar')
  })

  it('handles a selection within a single row', () => {
    expect(buildDragSelectionText(ENTRIES, { anchorId: 11, anchorOffset: 1, focusId: 11, focusOffset: 4 }))
      .toBe('rav')
  })

  it('clamps an endpoint whose row was trimmed out of the buffer', () => {
    // id 5 predates the buffer → clamp to the very start.
    expect(buildDragSelectionText(ENTRIES, { anchorId: 5, anchorOffset: 3, focusId: 11, focusOffset: 5 }))
      .toBe('alpha\nbravo')
    // id 99 is beyond the buffer → clamp to the very end.
    expect(buildDragSelectionText(ENTRIES, { anchorId: 12, anchorOffset: 0, focusId: 99, focusOffset: 1 }))
      .toBe('charlie\ndelta')
  })

  it('snaps an endpoint hidden by a filter change to the next present row', () => {
    // id 13 is not in the (filtered) list; insertion point is before id 14.
    expect(buildDragSelectionText(ENTRIES, { anchorId: 11, anchorOffset: 0, focusId: 13, focusOffset: 2 }))
      .toBe('bravo\ncharlie\n')
  })

  it('returns null when both endpoints are unresolvable', () => {
    expect(buildDragSelectionText([], { anchorId: 1, anchorOffset: 0, focusId: 2, focusOffset: 0 })).toBeNull()
  })
})

describe('pickRowIndexAtY (pointer → row for drag-focus clamping)', () => {
  const bands = [
    { index: 5, top: 100, bottom: 120 },
    { index: 6, top: 120, bottom: 140 },
    { index: 7, top: 140, bottom: 160 },
  ]

  it('returns the row whose band contains the y', () => {
    expect(pickRowIndexAtY(bands, 130)).toBe(6)
    expect(pickRowIndexAtY(bands, 100)).toBe(5)
  })

  it('snaps to the nearest edge row outside the bands', () => {
    expect(pickRowIndexAtY(bands, 50)).toBe(5)
    expect(pickRowIndexAtY(bands, 500)).toBe(7)
  })

  it('returns null for an empty list', () => {
    expect(pickRowIndexAtY([], 10)).toBeNull()
  })
})

describe('absOffsetInRow (char offset across highlight <mark> splits)', () => {
  it('accumulates text-node lengths before the target node', () => {
    const row = document.createElement('div')
    row.innerHTML = 'abc<mark>de</mark>fg'
    const fg = row.lastChild as Text
    expect(absOffsetInRow(row, fg, 1)).toBe(6)
    const de = row.querySelector('mark')!.firstChild as Text
    expect(absOffsetInRow(row, de, 2)).toBe(5)
  })

  it('treats an element-node endpoint as a child-index boundary', () => {
    const row = document.createElement('div')
    row.innerHTML = 'abc<mark>de</mark>fg'
    expect(absOffsetInRow(row, row, 2)).toBe(5) // after 'abc' + <mark>de</mark>
  })
})

describe('resolveRowEndpoint (selection endpoint → row index + offset)', () => {
  it('resolves a text node inside a data-index row', () => {
    const container = document.createElement('div')
    container.innerHTML = '<div data-index="3">hello</div>'
    const textNode = container.firstElementChild!.firstChild as Text
    expect(resolveRowEndpoint(container, textNode, 2)).toEqual({ index: 3, offset: 2 })
  })

  it('returns null for nodes outside any row', () => {
    const container = document.createElement('div')
    container.innerHTML = '<span>toolbar</span>'
    expect(resolveRowEndpoint(container, container.firstElementChild!.firstChild as Text, 1)).toBeNull()
  })
})
