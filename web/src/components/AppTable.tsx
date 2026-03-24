import type { AppDto } from '../lib/types'
import { StatusBadge } from './StatusBadge'

interface AppTableProps {
  apps: AppDto[]
}

export function AppTable({ apps }: AppTableProps) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-border text-left text-muted-foreground">
            <th className="pb-3 pr-4 font-medium">APP</th>
            <th className="pb-3 pr-4 font-medium">STATUS</th>
            <th className="pb-3 pr-4 font-medium">IMAGE</th>
            <th className="pb-3 pr-4 font-medium text-right">CPU</th>
            <th className="pb-3 font-medium text-right">MEMORY</th>
          </tr>
        </thead>
        <tbody>
          {apps.map((app) => (
            <tr key={app.name} className="border-b border-border/50 hover:bg-muted/30">
              <td className="py-2.5 pr-4 font-mono text-sm">{app.name}</td>
              <td className="py-2.5 pr-4">
                <StatusBadge status={app.status} />
              </td>
              <td className="py-2.5 pr-4 text-muted-foreground font-mono text-xs">{app.image}</td>
              <td className="py-2.5 pr-4 text-right font-mono text-xs text-muted-foreground">
                {app.cpu || '—'}
              </td>
              <td className="py-2.5 text-right font-mono text-xs text-muted-foreground">
                {app.memory || '—'}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
