import { gutter, GutterMarker, EditorView } from '@codemirror/view'
import { StateField, StateEffect, RangeSet, type EditorState } from '@codemirror/state'

export type LineKind = 'unchanged' | 'added' | 'changed'

// diffLineKinds returns, for each line of `current`, whether it is unchanged,
// changed, or added relative to `baseline`, via a line-level LCS. Baseline-only
// lines (deletions) have no current line to mark and are skipped.
export function diffLineKinds(baseline: string, current: string): LineKind[] {
  const a = baseline.replace(/\n$/, '').split('\n')
  const b = current.replace(/\n$/, '').split('\n')
  const n = a.length, m = b.length
  const dp: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0))
  for (let i = n - 1; i >= 0; i--)
    for (let j = m - 1; j >= 0; j--)
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1])
  const kinds: LineKind[] = []
  let i = 0, j = 0
  while (j < m) {
    if (i < n && a[i] === b[j]) { kinds.push('unchanged'); i++; j++ }
    else if (i < n && dp[i + 1][j] >= dp[i][j + 1]) { i++ }
    else { kinds.push(i < n ? 'changed' : 'added'); j++ }
  }
  return kinds
}

export const setBaseline = StateEffect.define<string>()

const baselineField = StateField.define<string>({
  create: () => '',
  update(value, tr) {
    for (const e of tr.effects) if (e.is(setBaseline)) return e.value
    return value
  },
})

class ChangeMarker extends GutterMarker {
  readonly kind: LineKind
  constructor(kind: LineKind) {
    super()
    this.kind = kind
  }
  override toDOM() {
    const el = document.createElement('div')
    el.className = `cm-change-marker cm-change-${this.kind}`
    return el
  }
}
const addedMarker = new ChangeMarker('added')
const changedMarker = new ChangeMarker('changed')

function markersFor(state: EditorState): RangeSet<GutterMarker> {
  const baseline = state.field(baselineField, false) ?? ''
  const kinds = diffLineKinds(baseline, state.doc.toString())
  const ranges = []
  for (let ln = 1; ln <= state.doc.lines; ln++) {
    const kind = kinds[ln - 1]
    if (kind === 'added') ranges.push(addedMarker.range(state.doc.line(ln).from))
    else if (kind === 'changed') ranges.push(changedMarker.range(state.doc.line(ln).from))
  }
  return RangeSet.of(ranges, true)
}

const changeMarkers = StateField.define<RangeSet<GutterMarker>>({
  create: markersFor,
  update(value, tr) {
    if (tr.docChanged || tr.effects.some((e) => e.is(setBaseline))) return markersFor(tr.state)
    return value
  },
})

const gutterTheme = EditorView.baseTheme({
  '.cm-change-gutter': { width: '3px', paddingLeft: '2px' },
  '.cm-change-marker': { width: '3px', height: '100%', borderRadius: '1px' },
  '.cm-change-added': { background: '#3fb950' }, // green
  '.cm-change-changed': { background: '#d29922' }, // amber
})

// changeGutterExtension builds the editor extension: a baseline holder + a
// gutter that paints a colored bar on added/changed lines (git-style). Update
// the baseline at runtime by dispatching setBaseline.of(text).
export function changeGutterExtension() {
  return [
    baselineField,
    changeMarkers,
    gutter({
      class: 'cm-change-gutter',
      markers: (view) => view.state.field(changeMarkers),
      initialSpacer: () => addedMarker,
    }),
    gutterTheme,
  ]
}
