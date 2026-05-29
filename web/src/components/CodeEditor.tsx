import { useMemo } from 'react'
import CodeMirror, { type Extension, EditorState, EditorView } from '@uiw/react-codemirror'
import { yaml } from '@codemirror/lang-yaml'
import { json } from '@codemirror/lang-json'
import { javascript } from '@codemirror/lang-javascript'
import { shell } from '@codemirror/legacy-modes/mode/shell'
import { dockerFile } from '@codemirror/legacy-modes/mode/dockerfile'
import { lua } from '@codemirror/legacy-modes/mode/lua'
import { clike } from '@codemirror/legacy-modes/mode/clike'
// StreamLanguage isn't re-exported from `@uiw/react-codemirror`, so pull it
// from the canonical `@codemirror/language` package. The lock-file already
// pins this via the legacy-modes peer chain.
import { indentUnit, StreamLanguage } from '@codemirror/language'

interface CodeEditorProps {
  value: string
  onChange?: (v: string) => void
  /** When true, render read-only (no edits, hide cursor). */
  readOnly?: boolean
  /** Filename or extension hint for syntax highlighting. */
  filename?: string
  /** Editor height in CSS units (e.g. "300px"). Default fills parent. */
  height?: string
  /** Auto-focus on mount — useful for editor windows. */
  autoFocus?: boolean
}

/**
 * Thin wrapper around CodeMirror 6 with syntax highlighting driven by file
 * extension. Matches Kotlin `EditorWindow` language table:
 *   yml/yaml → YAML, kt/java → C-like, js → JavaScript, json → JSON,
 *   lua → Lua, Dockerfile → Dockerfile, sh → Unix shell, anything else → plain.
 *
 * The editor exposes Ctrl+F / F3 search natively via CodeMirror; the parent
 * component does not need to wire a separate search bar. CodeMirror's built-in
 * search uses the same shortcuts the Kotlin EditorWindow supports (docs §5.4-5.5).
 */
export function CodeEditor({ value, onChange, readOnly = false, filename = '', height, autoFocus = false }: CodeEditorProps) {
  const extensions = useMemo<Extension[]>(() => {
    const lang = detectLanguage(filename)
    const exts: Extension[] = []
    if (lang) exts.push(lang)
    // 2-space tabs everywhere — Kotlin EditorWindow / our YAML conventions.
    // tabSize.of controls how a literal tab character renders, indentUnit.of
    // controls what gets inserted when the user invokes indent.
    exts.push(EditorState.tabSize.of(2))
    exts.push(indentUnit.of('  '))
    // Make CodeMirror's editor view paint its theme on the full parent
    // rectangle even when content is short — without this the cm-scroller
    // sizes to content height and the user sees a hard cut-off mid-window.
    exts.push(EditorView.theme({
      '&': { height: '100%' },
      '.cm-scroller': { minHeight: '100%' },
      '.cm-content': { minHeight: '100%' },
    }))
    return exts
  }, [filename])

  return (
    <CodeMirror
      value={value}
      height={height}
      extensions={extensions}
      onChange={(v) => onChange?.(v)}
      editable={!readOnly}
      autoFocus={autoFocus}
      theme="dark"
      basicSetup={{
        lineNumbers: true,
        highlightActiveLine: !readOnly,
        searchKeymap: true,
        foldGutter: true,
        autocompletion: false, // Kotlin EditorWindow has no autocomplete
      }}
    />
  )
}

function detectLanguage(filename: string): Extension | null {
  const lower = filename.toLowerCase()
  if (lower.endsWith('.yml') || lower.endsWith('.yaml')) return yaml()
  if (lower.endsWith('.json')) return json()
  if (lower.endsWith('.js') || lower.endsWith('.mjs') || lower.endsWith('.cjs')) return javascript()
  if (lower.endsWith('.kt') || lower.endsWith('.java')) {
    // Both use a C-like grammar — good enough for syntax colors. The clike
    // mode keyword/atom sets are word-keyed maps; passing strings would type-
    // error since 6.x.
    const isKotlin = lower.endsWith('.kt')
    return StreamLanguage.define(clike({
      name: isKotlin ? 'kotlin' : 'java',
      keywords: keywordSetForCLike(isKotlin ? 'kotlin' : 'java'),
      atoms: { true: true, false: true, null: true },
    }))
  }
  if (lower.endsWith('.lua')) return StreamLanguage.define(lua)
  if (lower.endsWith('.sh') || lower.endsWith('.bash')) return StreamLanguage.define(shell)
  if (lower.endsWith('dockerfile') || lower === 'dockerfile') return StreamLanguage.define(dockerFile)
  return null
}

function keywordSetForCLike(lang: 'kotlin' | 'java'): Record<string, true> {
  const kotlin = 'val var fun class object interface trait enum data sealed open override abstract internal private public protected return if else when while for do try catch finally throw in is as typeof package import constructor init companion this super by where suspend out vararg infix operator inline noinline crossinline reified delegate it'
  const java = 'class interface extends implements public private protected static final abstract void return if else while do switch case break continue new throw throws try catch finally synchronized volatile transient native package import this super instanceof'
  const words = lang === 'kotlin' ? kotlin : java
  return Object.fromEntries(words.split(/\s+/).map((w) => [w, true])) as Record<string, true>
}
