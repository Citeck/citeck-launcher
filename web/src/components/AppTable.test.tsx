import { render, screen } from '@testing-library/react'
import { describe, it, expect } from 'vitest'
import { MemoryRouter } from 'react-router'
import { AppTable } from './AppTable'
import type { AppDto } from '../lib/types'

const mockApps: AppDto[] = [
  { name: 'proxy', status: 'RUNNING', image: 'ecos-proxy:2.25', detached: false, cpu: '0.1%', memory: '32M', kind: 'THIRD_PARTY', ports: ['80:80'] },
  { name: 'gateway', status: 'STARTING', image: 'ecos-gateway:3.3', detached: false, cpu: '', memory: '', kind: 'CITECK_CORE' },
  { name: 'postgres', status: 'FAILED', image: 'postgres:17', detached: false, cpu: '', memory: '', kind: 'THIRD_PARTY' },
]

function renderWithRouter(ui: React.ReactElement) {
  return render(<MemoryRouter>{ui}</MemoryRouter>)
}

describe('AppTable', () => {
  it('renders all apps', () => {
    renderWithRouter(<AppTable apps={mockApps} />)
    expect(screen.getByText('proxy')).toBeInTheDocument()
    expect(screen.getByText('gateway')).toBeInTheDocument()
    expect(screen.getByText('postgres')).toBeInTheDocument()
  })

  it('renders column headers', () => {
    renderWithRouter(<AppTable apps={mockApps} />)
    // Multiple tables (one per group), each with headers
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

  it('renders app names as links', () => {
    renderWithRouter(<AppTable apps={mockApps} />)
    const proxyLink = screen.getByText('proxy').closest('a')
    expect(proxyLink).toHaveAttribute('href', '/apps/proxy')
  })

  it('renders empty table', () => {
    renderWithRouter(<AppTable apps={[]} />)
    // No groups rendered when empty
    expect(screen.queryByText('Name')).not.toBeInTheDocument()
  })

  it('renders group headers', () => {
    renderWithRouter(<AppTable apps={mockApps} />)
    expect(screen.getByText('Citeck Core')).toBeInTheDocument()
    expect(screen.getByText('Third Party')).toBeInTheDocument()
  })
})
