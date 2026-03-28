import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, beforeEach } from 'vitest'
import { MemoryRouter } from 'react-router'
import { AppTable } from './AppTable'
import { usePanelStore } from '../lib/panels'
import type { AppDto } from '../lib/types'

const mockApps: AppDto[] = [
  { name: 'proxy', status: 'RUNNING', image: 'ecos-proxy:2.25', cpu: '0.1%', memory: '32M', kind: 'THIRD_PARTY', ports: ['80:80'], edited: false, locked: false },
  { name: 'gateway', status: 'STARTING', image: 'ecos-gateway:3.3', cpu: '', memory: '', kind: 'CITECK_CORE', edited: false, locked: false },
  { name: 'postgres', status: 'FAILED', image: 'postgres:17', cpu: '', memory: '', kind: 'THIRD_PARTY', edited: false, locked: false },
]

function renderWithRouter(ui: React.ReactElement) {
  return render(<MemoryRouter>{ui}</MemoryRouter>)
}

beforeEach(() => {
  usePanelStore.getState().resetPanels()
})

describe('AppTable', () => {
  it('renders all apps', () => {
    renderWithRouter(<AppTable apps={mockApps} />)
    expect(screen.getByText('proxy')).toBeInTheDocument()
    expect(screen.getByText('gateway')).toBeInTheDocument()
    expect(screen.getByText('postgres')).toBeInTheDocument()
  })

  it('renders column headers', () => {
    renderWithRouter(<AppTable apps={mockApps} />)
    const names = screen.getAllByText('Name')
    expect(names.length).toBeGreaterThanOrEqual(1)
    const statuses = screen.getAllByText('Status')
    expect(statuses.length).toBeGreaterThanOrEqual(1)
  })

  it('renders status badges for each app', () => {
    renderWithRouter(<AppTable apps={mockApps} />)
    expect(screen.getByText('RUNNING')).toBeInTheDocument()
    expect(screen.getByText('STARTING')).toBeInTheDocument()
    expect(screen.getByText('FAILED')).toBeInTheDocument()
  })

  it('renders empty table with header', () => {
    renderWithRouter(<AppTable apps={[]} />)
    expect(screen.getByText('Name')).toBeInTheDocument()
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
    const logBtns = screen.getAllByTitle('Logs')
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
    const configBtns = screen.getAllByTitle('Config')
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
})
