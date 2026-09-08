import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { DependencyUpgradeBanner } from './DependencyUpgradeBanner'
import { useDashboardStore } from '../lib/store'
import { useDepsStore } from '../lib/depsStore'
import { useUpdateStore } from '../lib/updateStore'
import { isDesktopModeSync } from '../lib/desktop'
import { getDependencies } from '../lib/api'
import type { NamespaceDto } from '../lib/types'

vi.mock('../lib/api', () => ({
  getDependencies: vi.fn().mockResolvedValue({ items: [] }),
  // Reached only through the update dialog, which this suite never opens.
  getUpdateStatus: vi.fn(),
  checkUpdate: vi.fn(),
}))
vi.mock('../lib/desktop', () => ({ isDesktopModeSync: vi.fn(() => false) }))

function setUpgrades(upgrades: NamespaceDto['dependencyUpgrades']) {
  useDashboardStore.setState({
    namespace: { id: 'n', name: 'n', status: 'STOPPED', bundleRef: '', apps: [], dependencyUpgrades: upgrades } as NamespaceDto,
  })
}

const migratable = { id: 'postgres', app: 'postgres', from: 'postgres:17.5', to: 'postgres:18', migratable: true }
const needsLauncher = { id: 'rabbitmq', app: 'rabbitmq', from: 'rabbitmq:4.1.2-management', to: 'rabbitmq:4.2.9-management', migratable: false }

beforeEach(() => {
  vi.mocked(getDependencies).mockClear()
  vi.mocked(getDependencies).mockResolvedValue({ items: [] })
  vi.mocked(isDesktopModeSync).mockReturnValue(false)
  useDepsStore.setState({ dismissedKey: null, data: null, migration: null, result: null })
  useUpdateStore.setState({ status: null })
  useDashboardStore.setState({ namespace: null })
})

describe('DependencyUpgradeBanner', () => {
  it('renders nothing without upgrades', async () => {
    setUpgrades([])
    const { container } = render(<DependencyUpgradeBanner onDetails={() => {}} />)
    await waitFor(() => expect(getDependencies).toHaveBeenCalled())
    expect(container).toBeEmptyDOMElement()
  })

  it('distinguishes migratable from launcher-update upgrades', () => {
    setUpgrades([migratable, needsLauncher])
    render(<DependencyUpgradeBanner onDetails={() => {}} />)
    const text = screen.getByRole('status').textContent ?? ''
    expect(text).toContain('postgres:18')
    expect(text).toContain('rabbitmq:4.2.9-management')
    expect(text).toMatch(/launcher/i)
  })

  it('dismisses for the current set and comes back when the set changes', () => {
    setUpgrades([migratable])
    const { rerender } = render(<DependencyUpgradeBanner onDetails={() => {}} />)
    fireEvent.click(screen.getByLabelText(/dismiss/i))
    expect(screen.queryByRole('status')).toBeNull()
    setUpgrades([{ ...migratable, to: 'postgres:19' }])
    rerender(<DependencyUpgradeBanner onDetails={() => {}} />)
    expect(screen.getByRole('status')).toBeInTheDocument()
  })

  // A pending rollback freezes the dependency's version and refuses every new
  // migration until it succeeds — it outranks an available upgrade, and hiding
  // it would leave the user with no sign of a namespace that cannot move.
  it('shows a pending rollback instead of the upgrade offer, and never lets it be dismissed', async () => {
    setUpgrades([migratable])
    useDepsStore.setState({ dismissedKey: 'postgres:postgres:18' })
    vi.mocked(getDependencies).mockResolvedValue({ items: [], rollbackPending: 'a previous migration of postgres left a rollback pending' })
    render(<DependencyUpgradeBanner onDetails={() => {}} />)
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('rollback pending')
    expect(screen.queryByLabelText(/dismiss/i)).toBeNull()
  })

  it('offers the update dialog on desktop and names the CLI command in the server web UI', () => {
    setUpgrades([needsLauncher])
    render(<DependencyUpgradeBanner onDetails={() => {}} />)
    expect(screen.getByRole('status').textContent).toContain('citeck update')
    expect(screen.queryByRole('button', { name: /update the launcher/i })).toBeNull()

    vi.mocked(isDesktopModeSync).mockReturnValue(true)
    useUpdateStore.setState({ status: { currentVersion: '2.11.7', latestVersion: '2.12.0', available: true, applying: false } })
    render(<DependencyUpgradeBanner onDetails={() => {}} />)
    expect(screen.getAllByRole('button', { name: /update the launcher/i }).length).toBeGreaterThan(0)
  })

  it('opens the dependencies dialog from Details', () => {
    const onDetails = vi.fn()
    setUpgrades([migratable])
    render(<DependencyUpgradeBanner onDetails={onDetails} />)
    fireEvent.click(screen.getByRole('button', { name: /details/i }))
    expect(onDetails).toHaveBeenCalled()
  })
})
