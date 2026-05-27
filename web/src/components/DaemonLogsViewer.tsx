import { LogViewer } from './LogViewer'

interface DaemonLogsViewerProps {
  compact?: boolean
  active?: boolean
}

/**
 * Thin wrapper over {@link LogViewer} that targets the daemon log endpoint.
 * Routing the daemon logs through LogViewer gives them the same toolbar,
 * keyboard shortcuts, level filters, search and virtualisation as app logs.
 * The "appName" prop is reused as the window title slot — "daemon" surfaces
 * in download filenames via the source check inside LogViewer.
 */
export function DaemonLogsViewer({ compact = false, active = true }: DaemonLogsViewerProps) {
  return <LogViewer appName="daemon" source="daemon" compact={compact} active={active} />
}
