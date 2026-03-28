export function YamlViewer({ content }: { content: string }) {
  return (
    <pre className="rounded-md bg-background p-4 text-xs font-mono overflow-x-auto leading-relaxed">
      {content.split('\n').map((line, i) => (
        <YamlLine key={i} line={line} />
      ))}
    </pre>
  )
}

function YamlLine({ line }: { line: string }) {
  if (line.trim() === '') return <span>{'\n'}</span>

  if (line.trimStart().startsWith('#')) {
    return (
      <span>
        <span className="text-muted-foreground">{line}</span>
        {'\n'}
      </span>
    )
  }

  const match = line.match(/^(\s*)([\w.-]+)(:)(.*)$/)
  if (match) {
    const [, indent, key, colon, rest] = match
    return (
      <span>
        {indent}
        <span className="text-primary">{key}</span>
        <span className="text-muted-foreground">{colon}</span>
        <YamlValue value={rest} />
        {'\n'}
      </span>
    )
  }

  const listMatch = line.match(/^(\s*)(- )(.*)$/)
  if (listMatch) {
    const [, indent, dash, value] = listMatch
    return (
      <span>
        {indent}
        <span className="text-warning">{dash}</span>
        <YamlValue value={value} />
        {'\n'}
      </span>
    )
  }

  return (
    <span>
      {line}
      {'\n'}
    </span>
  )
}

function YamlValue({ value }: { value: string }) {
  const trimmed = value.trimStart()
  if (trimmed === '' || trimmed === '~' || trimmed === 'null') {
    return <span className="text-muted-foreground">{value}</span>
  }
  if (trimmed === 'true' || trimmed === 'false') {
    return <span className="text-warning">{value}</span>
  }
  if (/^\s*\d+(\.\d+)?$/.test(value)) {
    return <span className="text-success">{value}</span>
  }
  return <span className="text-foreground">{value}</span>
}
