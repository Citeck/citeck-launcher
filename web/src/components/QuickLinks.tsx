import type { LinkDto } from '../lib/types'

interface QuickLinksProps {
  links: LinkDto[]
}

export function QuickLinks({ links }: QuickLinksProps) {
  if (links.length === 0) return null
  const sorted = [...links].sort((a, b) => a.order - b.order)

  return (
    <div className="flex items-center gap-2 rounded border border-border bg-card px-3 py-1.5 text-xs">
      <span className="text-muted-foreground">Links:</span>
      {sorted.map((link) => (
        <a
          key={link.name}
          href={link.url}
          target="_blank"
          rel="noopener noreferrer"
          className="text-primary hover:underline"
        >
          {link.name}
        </a>
      ))}
    </div>
  )
}
