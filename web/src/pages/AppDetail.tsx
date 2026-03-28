import { useParams, Link } from 'react-router'
import { useDashboardStore } from '../lib/store'
import { StatusBadge } from '../components/StatusBadge'
import { AppDrawerContent } from '../components/AppDrawerContent'
import { AppConfigEditor } from '../components/AppConfigEditor'

export function AppDetail() {
  const { name } = useParams<{ name: string }>()
  const nsApps = useDashboardStore((s) => s.namespace?.apps)
  const appMeta = nsApps?.find((a) => a.name === name)

  if (!name) return null

  return (
    <div className="p-3 space-y-3">
      {/* Header */}
      <div className="flex items-center gap-2">
        <Link to="/" className="text-xs text-primary hover:underline">&larr; Dashboard</Link>
        <h1 className="text-base font-semibold">{name}</h1>
        {appMeta && <StatusBadge status={appMeta.status} />}
      </div>

      {/* Inspect details */}
      <AppDrawerContent appName={name} />

      {/* Config + files editor */}
      <AppConfigEditor appName={name} />
    </div>
  )
}
