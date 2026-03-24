import { render, screen } from '@testing-library/react'
import { describe, it, expect } from 'vitest'
import { MemoryRouter } from 'react-router'
import { AppTable } from './AppTable'
import type { AppDto } from '../lib/types'

const mockApps: AppDto[] = [
  { name: 'proxy', status: 'RUNNING', image: 'ecos-proxy:2.25', detached: false, cpu: '0.1%', memory: '32M' },
  { name: 'gateway', status: 'STARTING', image: 'ecos-gateway:3.3', detached: false, cpu: '', memory: '' },
  { name: 'postgres', status: 'FAILED', image: 'postgres:17', detached: false, cpu: '', memory: '' },
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
    expect(screen.getByText('APP')).toBeInTheDocument()
    expect(screen.getByText('STATUS')).toBeInTheDocument()
    expect(screen.getByText('IMAGE')).toBeInTheDocument()
    expect(screen.getByText('CPU')).toBeInTheDocument()
    expect(screen.getByText('MEMORY')).toBeInTheDocument()
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
    expect(screen.getByText('APP')).toBeInTheDocument()
  })

  it('shows dash for empty cpu/memory', () => {
    renderWithRouter(<AppTable apps={[mockApps[1]]} />)
    const dashes = screen.getAllByText('—')
    expect(dashes.length).toBe(2)
  })
})
