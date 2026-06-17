import { gutter, GutterMarker, EditorView, Decoration, type DecorationSet } from '@codemirror/view'
import { StateField, StateEffect, RangeSet, type EditorState, type Range } from '@codemirror/state'

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

// Line-background decorations for changed/added lines. A bg tint is far more
// visible than a 3px gutter bar — used together so the operator can't miss an
// edited line.
const addedLineDeco = Decoration.line({ class: 'cm-changed-line cm-changed-line-added' })
const changedLineDeco = Decoration.line({ class: 'cm-changed-line cm-changed-line-changed' })

function lineDecosFor(state: EditorState): DecorationSet {
  const baseline = state.field(baselineField, false) ?? ''
  const kinds = diffLineKinds(baseline, state.doc.toString())
  const ranges: Range<Decoration>[] = []
  for (let ln = 1; ln <= state.doc.lines; ln++) {
    const kind = kinds[ln - 1]
    const from = state.doc.line(ln).from
    if (kind === 'added') ranges.push(addedLineDeco.range(from))
    else if (kind === 'changed') ranges.push(changedLineDeco.range(from))
  }
  return Decoration.set(ranges, true)
}

const changedLines = StateField.define<DecorationSet>({
  create: lineDecosFor,
  update(value, tr) {
    if (tr.docChanged || tr.effects.some((e) => e.is(setBaseline))) return lineDecosFor(tr.state)
    return value
  },
  provide: (f) => EditorView.decorations.from(f),
})

const gutterTheme = EditorView.baseTheme({
  '.cm-change-gutter': { width: '4px', padding: '0' },
  '.cm-change-gutter .cm-gutterElement': { padding: '0' },
  '.cm-change-marker': { width: '4px', height: '100%', minHeight: '1em' },
  '.cm-change-added': { background: '#3fb950' }, // green
  '.cm-change-changed': { background: '#d29922' }, // amber
  '.cm-changed-line-added': { background: 'rgba(63, 185, 80, 0.13)' },
  '.cm-changed-line-changed': { background: 'rgba(210, 153, 34, 0.13)' },
})

// changeGutterExtension builds the editor extension: a baseline holder, a
// git-style colored gutter bar AND a line-background tint on added/changed lines
// (vs the generated baseline). Update the baseline at runtime by dispatching
// setBaseline.of(text).
export function changeGutterExtension() {
  return [
    baselineField,
    changeMarkers,
    changedLines,
    gutter({
      class: 'cm-change-gutter',
      markers: (view) => view.state.field(changeMarkers),
      initialSpacer: () => addedMarker,
    }),
    gutterTheme,
  ]
}
