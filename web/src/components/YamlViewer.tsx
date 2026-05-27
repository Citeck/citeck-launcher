import CodeMirror from '@uiw/react-codemirror'
import { yaml } from '@codemirror/lang-yaml'

interface YamlViewerProps {
  content: string
  /** Optional max height; without it the editor sizes to content. */
  height?: string
}

// Read-only viewer for YAML. The previous regex highlighter mis-rendered
// block scalars (`|` `>`), anchors/aliases, flow style, document markers,
// and multi-line strings — CodeMirror with @codemirror/lang-yaml handles
// all of them correctly. theme="dark" pulls oneDark re-exported by
// @uiw/react-codemirror, matching the app's Darcula shell.
export function YamlViewer({ content, height }: YamlViewerProps) {
  return (
    <CodeMirror
      value={content}
      height={height}
      extensions={[yaml()]}
      editable={false}
      theme="dark"
      basicSetup={{
        lineNumbers: false,
        foldGutter: false,
        highlightActiveLine: false,
        searchKeymap: true,
        autocompletion: false,
      }}
    />
  )
}
