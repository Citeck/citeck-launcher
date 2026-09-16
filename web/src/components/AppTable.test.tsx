import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, beforeEach } from 'vitest'
import { MemoryRouter } from 'react-router'
import { AppTable } from './AppTable'
import { usePanelStore } from '../lib/panels'
import { useDashboardStore } from '../lib/store'
import type { AppDto, NamespaceDto } from '../lib/types'

const mockApps: AppDto[] = [
  { name: 'proxy', status: 'RUNNING', image: 'ecos-proxy:2.25', cpu: '0.1%', memory: '32M', kind: 'THIRD_PARTY', ports: ['80:80'], edited: false, locked: false },
  { name: 'gateway', status: 'STARTING', image: 'ecos-gateway:3.3', cpu: '', memory: '', kind: 'CITECK_CORE', edited: false, locked: false },
  { name: 'postgres', status: 'FAILED', image: 'postgres:17', cpu: '', memory: '', kind: 'THIRD_PARTY', edited: false, locked: false },
]

function renderWithRouter(ui: React.ReactElement) {
  return render(<MemoryRouter>{ui}</MemoryRouter>)
}

// The row's start button needs the NAMESPACE status, not the app's: the
// daemon refuses a per-app start while the namespace is not running.
function setNamespaceStatus(status: NamespaceDto['status'] | null) {
  useDashboardStore.setState({
    namespace: status === null
      ? null
      : { id: 'n', name: 'n', status, bundleRef: '', apps: [] } as NamespaceDto,
  })
}

beforeEach(() => {
  usePanelStore.getState().resetPanels()
  setNamespaceStatus('RUNNING')
})

describe('AppTable', () => {
  it('renders all apps', () => {
    renderWithRouter(<AppTable apps={mockApps} />)
    expect(screen.getByText('proxy')).toBeInTheDocument()
    expect(screen.getByText('gateway')).toBeInTheDocument()
    expect(screen.getByText('postgres')).toBeInTheDocument()
  })

  it('renders no column-header row (labels removed — the table is self-evident)', () => {
    renderWithRouter(<AppTable apps={mockApps} />)
    expect(screen.queryByText('Name')).not.toBeInTheDocument()
    expect(screen.queryByText('Status')).not.toBeInTheDocument()
  })

  it('renders status badges for each app', () => {
    renderWithRouter(<AppTable apps={mockApps} />)
    expect(screen.getByText('Running')).toBeInTheDocument()
    expect(screen.getByText('Starting')).toBeInTheDocument()
    expect(screen.getByText('Failed')).toBeInTheDocument()
  })

  it('renders an empty table without crashing', () => {
    const { container } = renderWithRouter(<AppTable apps={[]} />)
    expect(container.querySelector('table')).toBeInTheDocument()
  })

  it('renders group headers', () => {
    renderWithRouter(<AppTable apps={mockApps} />)
    expect(screen.getByText('Citeck Core')).toBeInTheDocument()
    expect(screen.getByText('Third Party')).toBeInTheDocument()
  })

  it('clicking app name opens drawer', () => {
    renderWithRouter(<AppTable apps={mockApps} />)
    const proxyBtn = screen.getByText('proxy')
    fireEvent.click(proxyBtn)
    expect(usePanelStore.getState().drawerAppName).toBe('proxy')
  })

  it('clicking logs icon opens log tab in bottom panel', () => {
    renderWithRouter(<AppTable apps={mockApps} />)
    const logBtns = screen.getAllByTitle(/^Logs:/)
    fireEvent.click(logBtns[0]) // first app in sorted order
    const { bottomTabs, activeBottomTabId, bottomPanelOpen } = usePanelStore.getState()
    expect(bottomTabs).toHaveLength(1)
    expect(bottomTabs[0].type).toBe('logs')
    expect(bottomTabs[0].appName).toBeDefined()
    expect(activeBottomTabId).toBe(bottomTabs[0].id)
    expect(bottomPanelOpen).toBe(true)
  })

  it('clicking config icon opens app-config tab in bottom panel', () => {
    renderWithRouter(<AppTable apps={mockApps} />)
    const configBtns = screen.getAllByTitle(/Left [Cc]lick/)
    fireEvent.click(configBtns[0])
    const { bottomTabs } = usePanelStore.getState()
    expect(bottomTabs).toHaveLength(1)
    expect(bottomTabs[0].type).toBe('app-config')
    expect(bottomTabs[0].appName).toBeDefined()
  })

  it('highlights row when highlightedApp matches', () => {
    renderWithRouter(<AppTable apps={mockApps} highlightedApp="proxy" />)
    const proxyRow = screen.getByText('proxy').closest('tr')
    expect(proxyRow?.className).toContain('bg-primary')
  })

  it('non-highlighted rows have default hover style', () => {
    renderWithRouter(<AppTable apps={mockApps} highlightedApp="proxy" />)
    const gatewayRow = screen.getByText('gateway').closest('tr')
    expect(gatewayRow?.className).not.toContain('bg-primary')
    expect(gatewayRow?.className).toContain('hover:bg-accent')
  })

  // The daemon sends {app, status} pairs and no sentence, so the row is the
  // only place the hold is ever worded. A row that renders nothing looks
  // exactly like an app nobody is waiting on.
  it('a held app says which dependency holds it, with the status translated', () => {
    const held: AppDto = {
      name: 'rag', status: 'DEPS_WAITING', image: 'rag:1', cpu: '', memory: '',
      kind: 'THIRD_PARTY', waitingFor: [{ app: 'qdrant', status: 'STOPPED' }],
    }
    renderWithRouter(<AppTable apps={[held]} />)
    expect(screen.getByText('Waiting for: qdrant (Stopped)')).toBeInTheDocument()
  })

  // A hold routinely crosses Kind groups — proxy is rendered from the core
  // group, alfresco from the additional one — and the row is rendered by
  // GroupRows, which only has ITS group's apps. Handing the walk that narrow
  // list makes it fail to resolve the intermediate app and report IT as the
  // root: exactly the "start an app you cannot start" sentence the walk exists
  // to remove, and one that contradicts what the drawer says about the same app.
  it('names the detached root even when the hold crosses Kind groups', () => {
    const apps: AppDto[] = [
      { name: 'alfresco-postgres', status: 'STOPPED', image: 'postgres:9.4', cpu: '', memory: '',
        kind: 'THIRD_PARTY', edited: false, locked: false },
      { name: 'alfresco', status: 'DEPS_WAITING', image: 'alfresco:7', cpu: '', memory: '',
        kind: 'CITECK_ADDITIONAL', edited: false, locked: false, held: true,
        waitingFor: [{ app: 'alfresco-postgres', status: 'STOPPED' }] },
      { name: 'proxy', status: 'DEPS_WAITING', image: 'ecos-proxy:2.25', cpu: '', memory: '',
        kind: 'CITECK_CORE', edited: false, locked: false, held: true,
        waitingFor: [{ app: 'alfresco', status: 'DEPS_WAITING' }] },
    ]

    renderWithRouter(<AppTable apps={apps} />)

    expect(screen.getAllByText('Waiting for: alfresco-postgres (Stopped)')).toHaveLength(2)
    expect(screen.queryByText(/Waiting for: alfresco \(/)).not.toBeInTheDocument()
  })

  it('falls back to statusText when nothing is holding the app', () => {
    const failed: AppDto = {
      name: 'rag', status: 'START_FAILED', image: 'rag:1', cpu: '', memory: '',
      kind: 'THIRD_PARTY', statusText: 'container exited with code 1',
    }
    renderWithRouter(<AppTable apps={[failed]} />)
    expect(screen.getByText('container exited with code 1')).toBeInTheDocument()
  })
  // A stopped namespace has no runtime loop, so a per-app start cannot be
  // carried out: the daemon answers NAMESPACE_NOT_RUNNING. Before this the
  // button was enabled and pressing it produced either "app not found" (the
  // registry is built when the namespace starts) or a silent no-op — both
  // indistinguishable from success. Same treatment the Restart item already
  // gets: disabled, with the reason as its tooltip.
  it('disables the start button while the namespace is not running', () => {
    setNamespaceStatus('STOPPED')
    const stopped: AppDto = {
      name: 'postgres', status: 'STOPPED', image: 'postgres:18', cpu: '', memory: '',
      kind: 'THIRD_PARTY', edited: false, locked: false,
    }
    renderWithRouter(<AppTable apps={[stopped]} />)

    const btn = screen.getByTitle(
      'The namespace is not running — start it first, then start this application on its own.')
    expect(btn).toBeDisabled()
  })

  it('enables the start button once the namespace is running', () => {
    setNamespaceStatus('RUNNING')
    const stopped: AppDto = {
      name: 'postgres', status: 'STOPPED', image: 'postgres:18', cpu: '', memory: '',
      kind: 'THIRD_PARTY', edited: false, locked: false,
    }
    renderWithRouter(<AppTable apps={[stopped]} />)

    expect(screen.getByTitle('Start')).toBeEnabled()
  })

  // STARTING and STALLED are the other two states in which the loop is alive:
  // a namespace that came up STALLED is exactly when an operator reaches for
  // one app's start button.
  // A detached app is the exception: its start button is an ATTACH, which the
  // daemon carries out while the namespace is stopped (the app then starts with
  // the namespace). rag ships detached by the default workspace template, so
  // this is the ordinary way it gets switched on.
  it('keeps the start button live for a detached app on a stopped namespace', () => {
    setNamespaceStatus('STOPPED')
    const detached: AppDto = {
      name: 'rag', status: 'STOPPED', image: 'citeck-rag:1.2.2', cpu: '', memory: '',
      kind: 'CITECK_ADDITIONAL', edited: false, locked: false, detached: true,
    }
    renderWithRouter(<AppTable apps={[detached]} />)

    expect(screen.getByTitle('Start')).toBeEnabled()
  })

  it('enables the start button on a stalled namespace', () => {
    setNamespaceStatus('STALLED')
    const stopped: AppDto = {
      name: 'postgres', status: 'STOPPED', image: 'postgres:18', cpu: '', memory: '',
      kind: 'THIRD_PARTY', edited: false, locked: false,
    }
    renderWithRouter(<AppTable apps={[stopped]} />)

    expect(screen.getByTitle('Start')).toBeEnabled()
  })
})
