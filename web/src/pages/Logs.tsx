import { useParams, Link } from 'react-router'
import { LogViewer } from '../components/LogViewer'

export function Logs() {
  const { name } = useParams<{ name: string }>()

  if (!name) return null

  return (
    <div className="flex flex-col h-[calc(100vh-100px)]">
      <div className="flex items-center justify-between mb-4">
        <div>
          <Link to={`/apps/${name}`} className="text-sm text-primary hover:underline">
            &larr; Back to {name}
          </Link>
          <h1 className="text-2xl font-semibold mt-1">Logs: {name}</h1>
        </div>
      </div>
      <div className="flex-1 min-h-0">
        <LogViewer appName={name} compact />
      </div>
    </div>
  )
}
