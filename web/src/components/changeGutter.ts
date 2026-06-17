import { gutter, GutterMarker, EditorView, Decoration, type DecorationSet, type BlockInfo } from '@codemirror/view'
import { StateField, StateEffect, RangeSet, type EditorState, type Range } from '@codemirror/state'
import { undo } from '@codemirror/commands'
import { toast } from '../lib/toast'
import { t } from '../lib/i18n'

export type LineKind = 'unchanged' | 'added' | 'changed'

// LineOp describes, for one CURRENT line, how it relates to the baseline:
//   - unchanged: identical baseline line exists (base = that text).
//   - changed:   replaces a baseline line (base = the baseline text to restore).
//   - added:     no baseline counterpart (base = null; revert ⇒ delete the line).
export interface LineOp {
  kind: LineKind
  base: string | null
}

function splitLines(s: string): string[] {
  if (s === '') return []
  return s.replace(/\n$/, '').split('\n')
}

// alignLines aligns current lines to baseline lines via a line-level LCS, then
// classifies each current line. Deleted baseline lines (no current counterpart)
// are paired FIFO with inserted current lines as "changed" so a value edit reads
// as a change (with the baseline text to restore), while genuinely new lines
// read as "added".
export function alignLines(baseline: string, current: string): LineOp[] {
  const a = splitLines(baseline)
  const b = splitLines(current)
  const n = a.length, m = b.length
  const dp: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0))
  for (let i = n - 1; i >= 0; i--)
    for (let j = m - 1; j >= 0; j--)
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1])

  const ops: LineOp[] = new Array(m)
  const pendingDeleted: string[] = [] // baseline lines with no match yet
  let i = 0, j = 0
  while (j < m || i < n) {
    if (i < n && j < m && a[i] === b[j]) {
      pendingDeleted.length = 0 // genuine deletions; nothing to attach them to
      ops[j] = { kind: 'unchanged', base: a[i] }
      i++; j++
    } else if (i < n && (j >= m || dp[i + 1][j] >= dp[i][j + 1])) {
      pendingDeleted.push(a[i]) // baseline-only line
      i++
    } else {
      // current-only line: pair with a pending deleted baseline line if any.
      const base = pendingDeleted.length > 0 ? pendingDeleted.shift()! : null
      ops[j] = { kind: base !== null ? 'changed' : 'added', base }
      j++
    }
  }
  return ops
}

// diffLineKinds returns just the per-current-line kind (used for markers/decos).
export function diffLineKinds(baseline: string, current: string): LineKind[] {
  return alignLines(baseline, current).map((o) => o.kind)
}

export const setBaseline = StateEffect.define<string>()

const baselineField = StateField.define<string>({
  create: () => '',
  update(value, tr) {
    for (const e of tr.effects) if (e.is(setBaseline)) return e.value
    return value
  },
})

// Tooltip shown on clickable revert markers; set per-editor by the factory.
let revertTitle = ''

class ChangeMarker extends GutterMarker {
  readonly kind: LineKind
  constructor(kind: LineKind) {
    super()
    this.kind = kind
  }
  override toDOM() {
    const el = document.createElement('div')
    el.className = `cm-change-marker cm-change-${this.kind}`
    if (revertTitle) el.title = revertTitle
    return el
  }
}
const addedMarker = new ChangeMarker('added')
const changedMarker = new ChangeMarker('changed')

function markersFor(state: EditorState): RangeSet<GutterMarker> {
  const baseline = state.field(baselineField, false) ?? ''
  // No baseline yet (not loaded / not applicable) → don't paint the whole file
  // as "added". Markers appear once a real baseline is set.
  if (baseline === '') return RangeSet.empty
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
// visible than a thin gutter bar — used together so an edited line can't be
// missed.
const addedLineDeco = Decoration.line({ class: 'cm-changed-line cm-changed-line-added' })
const changedLineDeco = Decoration.line({ class: 'cm-changed-line cm-changed-line-changed' })

function lineDecosFor(state: EditorState): DecorationSet {
  const baseline = state.field(baselineField, false) ?? ''
  if (baseline === '') return Decoration.none
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

// revertLineAt reverts the clicked line to its baseline: a changed line is
// replaced with the baseline text, an added line is deleted. Returns true when
// it acted (so the gutter swallows the click).
function revertLineAt(view: EditorView, block: BlockInfo): boolean {
  const baseline = view.state.field(baselineField, false) ?? ''
  const num = view.state.doc.lineAt(block.from).number
  const op = alignLines(baseline, view.state.doc.toString())[num - 1]
  if (!op || op.kind === 'unchanged') return false
  const line = view.state.doc.line(num)
  const doc = view.state.doc.toString()
  // Range to replace and the resulting text — used both to compute the expected
  // document (to verify the native edit) and for the programmatic fallback.
  let from: number, to: number, insert: string
  if (op.kind === 'added') {
    // Delete the whole line plus one adjoining newline (prefer the preceding one).
    from = line.from > 0 ? line.from - 1 : line.from
    to = line.from > 0 ? line.to : Math.min(line.to + 1, doc.length)
    insert = ''
  } else {
    from = line.from
    to = line.to
    insert = op.base ?? ''
  }
  const expected = doc.slice(0, from) + insert + doc.slice(to)

  // Prefer a NATIVE edit (execCommand): the contenteditable records it in the
  // webview's native undo history, so the native Ctrl+Z — which JS can't
  // intercept on this webview for Latin layouts — undoes it for free. CM's DOM
  // observer syncs the change into its own state. If the webview/CM don't apply
  // it cleanly, fall back to a programmatic dispatch (still undoable via the
  // toast button / the document-level Ctrl+Z handler on layouts that reach JS).
  view.focus()
  view.dispatch({ selection: { anchor: from, head: to } })
  let nativeOK: boolean
  try {
    nativeOK = insert === '' ? document.execCommand('delete') : document.execCommand('insertText', false, insert)
  } catch {
    nativeOK = false
  }
  if (!nativeOK || view.state.doc.toString() !== expected) {
    view.dispatch({ changes: { from, to, insert }, selection: { anchor: from } })
  }
  view.focus()
  // Reliable Undo affordance regardless of whether the native edit above stuck:
  // a button click is never intercepted by the webview the way Ctrl+Z is.
  toast(t('appConfig.lineReverted'), 'info', {
    label: t('appConfig.undo'),
    onClick: () => {
      undo(view)
      view.focus()
    },
  })
  return true
}

const gutterTheme = EditorView.baseTheme({
  '.cm-change-gutter': { width: '4px', padding: '0', cursor: 'pointer' },
  '.cm-change-gutter .cm-gutterElement': { padding: '0' },
  '.cm-change-marker': { width: '4px', height: '100%', minHeight: '1em' },
  '.cm-change-added': { background: '#3fb950' }, // green
  '.cm-change-changed': { background: '#d29922' }, // amber
  '.cm-changed-line-added': { background: 'rgba(63, 185, 80, 0.13)' },
  '.cm-changed-line-changed': { background: 'rgba(210, 153, 34, 0.13)' },
})

// changeGutterExtension builds the editor extension: a baseline holder, a
// git-style colored gutter bar AND a line-background tint on added/changed lines
// (vs the generated baseline), plus click-to-revert on the gutter marker.
// `revertLabel` is the marker tooltip. Update the baseline at runtime by
// dispatching setBaseline.of(text).
export function changeGutterExtension(revertLabel = '') {
  revertTitle = revertLabel
  return [
    baselineField,
    changeMarkers,
    changedLines,
    gutter({
      class: 'cm-change-gutter',
      markers: (view) => view.state.field(changeMarkers),
      initialSpacer: () => addedMarker,
      domEventHandlers: {
        mousedown: (view, block, event) => {
          if ((event as MouseEvent).button !== 0) return false
          return revertLineAt(view, block)
        },
      },
    }),
    gutterTheme,
  ]
}
