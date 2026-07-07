import type { LogEntry } from '../hooks/useLogStream'

/**
 * Logical bounds of a mouse drag-selection over the virtualized log list,
 * tracked by ENTRY ID + character offset instead of DOM nodes. The native
 * browser selection only spans rows that are currently mounted — rows
 * scrolled past the overscan window unmount and silently leave the
 * selection, so a copy served from the DOM contains just the visible slice.
 * These bounds survive unmounting; the copy handler rebuilds the full text
 * from the entry buffer (see buildDragSelectionText).
 */
export interface DragSelection {
  anchorId: number
  anchorOffset: number
  focusId: number
  focusOffset: number
}

/** Binary search over the ascending entry ids: exact index or -(insertion+1). */
function findId(entries: LogEntry[], id: number): number {
  let lo = 0
  let hi = entries.length - 1
  while (lo <= hi) {
    const mid = (lo + hi) >> 1
    if (entries[mid].id === id) return mid
    if (entries[mid].id < id) lo = mid + 1
    else hi = mid - 1
  }
  return -(lo + 1)
}

/**
 * Resolves one logical endpoint to (entry index, char offset), snapping a
 * missing id (trimmed off the buffer, or hidden by a filter change) to its
 * insertion point. Returns null only when the list is empty.
 */
function resolveEndpoint(entries: LogEntry[], id: number, offset: number): { index: number; offset: number } | null {
  if (entries.length === 0) return null
  const i = findId(entries, id)
  if (i >= 0) return { index: i, offset }
  const insert = -i - 1
  if (insert >= entries.length) {
    const last = entries.length - 1
    return { index: last, offset: entries[last].text.length }
  }
  return { index: insert, offset: 0 }
}

/**
 * Rebuilds the selected text from the entry buffer for a drag-tracked
 * selection. Direction-agnostic (an upward drag has focus before anchor).
 * Returns null when nothing can be resolved (empty list) — the caller then
 * falls back to the native DOM copy.
 */
export function buildDragSelectionText(entries: LogEntry[], ds: DragSelection): string | null {
  const a = resolveEndpoint(entries, ds.anchorId, ds.anchorOffset)
  const f = resolveEndpoint(entries, ds.focusId, ds.focusOffset)
  if (!a || !f) return null
  const [start, end] = a.index < f.index || (a.index === f.index && a.offset <= f.offset) ? [a, f] : [f, a]
  if (start.index === end.index) {
    return entries[start.index].text.slice(start.offset, end.offset)
  }
  const parts: string[] = [entries[start.index].text.slice(start.offset)]
  for (let i = start.index + 1; i < end.index; i++) parts.push(entries[i].text)
  parts.push(entries[end.index].text.slice(0, end.offset))
  return parts.join('\n')
}

/**
 * Absolute character offset of a (node, offset) DOM position within a row
 * element whose content may be split across text nodes by search-highlight
 * <mark> elements. An element-node position is interpreted the DOM way: the
 * offset counts child nodes, so the result is the text length of the children
 * before it.
 */
export function absOffsetInRow(row: HTMLElement, node: Node, offset: number): number {
  if (node === row) {
    let acc = 0
    for (let i = 0; i < offset && i < row.childNodes.length; i++) {
      acc += row.childNodes[i].textContent?.length ?? 0
    }
    return acc
  }
  let acc = 0
  const walker = document.createTreeWalker(row, NodeFilter.SHOW_TEXT)
  let n: Node | null
  while ((n = walker.nextNode())) {
    if (n === node) return acc + offset
    acc += (n.textContent ?? '').length
  }
  // Node not under this row (shouldn't happen once the caller matched the
  // row via closest('[data-index]')) — treat as row start.
  return 0
}

export interface RowBand {
  index: number
  top: number
  bottom: number
}

/**
 * Nearest row for a pointer Y during a drag: the band containing y, else the
 * closest edge band. Used to clamp the selection focus when the engine's
 * caret hit-test misresolves (WebKitGTK can land in the wrapper's FIRST
 * position for points in the empty space right of short lines / blank rows,
 * visually selecting everything up to the top of the list).
 */
export function pickRowIndexAtY(bands: RowBand[], y: number): number | null {
  let best: RowBand | null = null
  let bestDist = Infinity
  for (const b of bands) {
    if (y >= b.top && y < b.bottom) return b.index
    const d = y < b.top ? b.top - y : y - b.bottom
    if (d < bestDist) {
      bestDist = d
      best = b
    }
  }
  return best ? best.index : null
}

/**
 * Maps a selection endpoint (node, offset) to the containing virtualized
 * row's data-index and the absolute char offset within that row's text.
 * Returns null when the node is not inside a row of `container`.
 */
export function resolveRowEndpoint(container: HTMLElement, node: Node, offset: number): { index: number; offset: number } | null {
  const el = node instanceof Element ? node : node.parentElement
  const row = el?.closest('[data-index]') as HTMLElement | null
  if (!row || !container.contains(row)) return null
  const index = Number(row.getAttribute('data-index'))
  if (!Number.isFinite(index)) return null
  return { index, offset: absOffsetInRow(row, node, offset) }
}
