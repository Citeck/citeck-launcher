import { StateEffect, StateField, RangeSetBuilder, type Extension } from '@codemirror/state'
import { Decoration, type DecorationSet, EditorView } from '@codemirror/view'

/** A single match range in the document. */
export interface Match {
  from: number
  to: number
}

/**
 * Custom search highlighting for CodeEditor. We deliberately do NOT use
 * @codemirror/search's built-in panel — it mounts a crude bottom bar with a
 * perceptible delay. Instead a React toolbar (CodeEditor) computes matches and
 * dispatches them here as decorations, so highlighting is instant and styled
 * to the app theme. The current match gets a distinct "active" decoration.
 */
export const setSearchMatches = StateEffect.define<{ matches: Match[]; current: number }>()

const matchMark = Decoration.mark({ class: 'cm-search-match' })
const activeMark = Decoration.mark({ class: 'cm-search-match-active' })

const searchField = StateField.define<DecorationSet>({
  create() {
    return Decoration.none
  },
  update(deco, tr) {
    // Keep existing highlights aligned across edits until the next recompute.
    deco = deco.map(tr.changes)
    for (const e of tr.effects) {
      if (e.is(setSearchMatches)) {
        const builder = new RangeSetBuilder<Decoration>()
        e.value.matches.forEach((m, i) => {
          builder.add(m.from, m.to, i === e.value.current ? activeMark : matchMark)
        })
        deco = builder.finish()
      }
    }
    return deco
  },
  provide: (f) => EditorView.decorations.from(f),
})

const searchTheme = EditorView.baseTheme({
  '.cm-search-match': {
    backgroundColor: 'rgba(77, 156, 246, 0.25)', // primary @ low alpha
    borderRadius: '2px',
  },
  '.cm-search-match-active': {
    backgroundColor: 'rgba(232, 196, 77, 0.55)', // warning/amber — the focused hit
    borderRadius: '2px',
    outline: '1px solid rgba(232, 196, 77, 0.95)',
  },
})

/** CodeMirror extension that renders search-match decorations. */
export function searchHighlighter(): Extension {
  return [searchField, searchTheme]
}

/**
 * Compute all match ranges of `query` in `doc`. Plain substring by default;
 * regex when `opts.regexp`. Config files are small, so a straight JS scan is
 * cheaper and more predictable than wiring a CodeMirror SearchCursor.
 */
export function computeMatches(
  doc: string,
  query: string,
  opts: { caseSensitive: boolean; regexp: boolean },
): Match[] {
  if (!query) return []
  const matches: Match[] = []
  if (opts.regexp) {
    let re: RegExp
    try {
      re = new RegExp(query, opts.caseSensitive ? 'g' : 'gi')
    } catch {
      return [] // invalid regex while typing — show no matches, no crash
    }
    let m: RegExpExecArray | null
    let guard = 0
    while ((m = re.exec(doc)) !== null && guard++ < 100000) {
      if (m[0] === '') {
        re.lastIndex++ // avoid an infinite loop on zero-width matches
        continue
      }
      matches.push({ from: m.index, to: m.index + m[0].length })
    }
  } else {
    const hay = opts.caseSensitive ? doc : doc.toLowerCase()
    const needle = opts.caseSensitive ? query : query.toLowerCase()
    let idx = hay.indexOf(needle)
    while (idx !== -1) {
      matches.push({ from: idx, to: idx + needle.length })
      idx = hay.indexOf(needle, idx + needle.length)
    }
  }
  return matches
}
