import { render, screen, act, waitFor } from '@testing-library/react'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ContentStorageBanner } from './ContentStorageBanner'
import { useDashboardStore } from '../lib/store'
import { useContentStorageBannerStore } from '../lib/contentStorage'
import { postContentStorageProvision } from '../lib/api'
import type { ContentStorageDto, NamespaceDto } from '../lib/types'

vi.mock('../lib/api', () => ({
  postContentStorageProvision: vi.fn().mockResolvedValue({ success: true, message: '' }),
}))
vi.mock('../lib/toast', () => ({ toast: vi.fn() }))

const fetchData = vi.fn().mockResolvedValue(undefined)

function setNamespace(cs: ContentStorageDto | undefined, status: NamespaceDto['status'] = 'RUNNING', id = 'n') {
  useDashboardStore.setState({
    namespace: { id, name: id, status, bundleRef: '', apps: [], contentStorage: cs } as NamespaceDto,
    fetchData,
  })
}

const s3 = (patch: Partial<ContentStorageDto>): ContentStorageDto =>
  ({ type: 'S3', providerApp: 'seaweedfs', state: 'pending', ...patch })

beforeEach(() => {
  useContentStorageBannerStore.setState({ dismissedNs: null })
  vi.mocked(postContentStorageProvision).mockClear()
  fetchData.mockClear()
})

describe('ContentStorageBanner', () => {
  // A DB namespace — every namespace that predates the option — carries no
  // contentStorage at all, and must look exactly as it did.
  it('renders nothing for a DB namespace', () => {
    setNamespace(undefined)
    const { container } = render(<ContentStorageBanner />)
    expect(container.firstChild).toBeNull()
  })

  it('renders nothing once the connection is settled, either way', () => {
    for (const state of ['connected', 'admin-managed']) {
      setNamespace(s3({ state }))
      const { container, unmount } = render(<ContentStorageBanner />)
      expect(container.firstChild).toBeNull()
      unmount()
    }
  })

  it('shows progress as information while the platform comes up', () => {
    setNamespace(s3({ state: 'waiting' }))
    render(<ContentStorageBanner />)
    const status = screen.getByRole('status')
    expect(status.textContent).toContain('seaweedfs')
    expect(status.textContent).toContain('waiting for the platform')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  // Every S3 namespace reads "pending" while stopped — the daemon re-derives
  // the connection after each start — so on a stopped namespace the banner
  // would claim progress that is not happening.
  it('stays out of the way on a stopped namespace', () => {
    setNamespace(s3({ state: 'pending' }), 'STOPPED')
    const { container } = render(<ContentStorageBanner />)
    expect(container.firstChild).toBeNull()
  })

  it('lets the user hide the progress banner for this namespace only', () => {
    setNamespace(s3({ state: 'provisioning' }))
    const { rerender } = render(<ContentStorageBanner />)
    act(() => { screen.getByRole('button', { name: 'Hide' }).click() })
    expect(screen.queryByRole('status')).toBeNull()

    setNamespace(s3({ state: 'provisioning' }), 'RUNNING', 'other')
    rerender(<ContentStorageBanner />)
    expect(screen.getByRole('status')).toBeDefined()
  })

  // A failed connection is broken right now: it is not dismissible, even when
  // the user hid the progress banner that preceded it, and it carries Retry.
  it('reports a failed connection with the daemon’s reason and a Retry that asks the daemon', async () => {
    setNamespace(s3({ state: 'provisioning' }))
    const { rerender } = render(<ContentStorageBanner />)
    act(() => { screen.getByRole('button', { name: 'Hide' }).click() })

    setNamespace(s3({ state: 'error', error: 'gateway did not answer within 15 minutes' }))
    rerender(<ContentStorageBanner />)
    const alert = screen.getByRole('alert')
    expect(alert.textContent).toContain('gateway did not answer within 15 minutes')
    expect(screen.queryByRole('button', { name: 'Hide' })).toBeNull()

    await act(async () => { screen.getByRole('button', { name: /Retry/ }).click() })
    expect(postContentStorageProvision).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(fetchData).toHaveBeenCalled())
  })

  // A generation problem is configuration: no retry can fix it, so it gets no
  // Retry button, and it outranks whatever the connection state says.
  it('reports a provider the generation could not produce, without Retry, over any state', () => {
    setNamespace(s3({ state: 'error', error: 'boom', problem: 'PROVIDER_NOT_GENERATED' }))
    render(<ContentStorageBanner />)
    const alert = screen.getByRole('alert')
    expect(alert.textContent).toContain('seaweedfs was left out of the namespace')
    expect(alert.textContent).not.toContain('boom')
    expect(screen.queryByRole('button')).toBeNull()
  })

  // The gate is the four ECOS apps AND the provider: with the provider stopped
  // (warned about, never refused) the other four are all running, and the
  // banner must name what it is still waiting for.
  it('names the provider among the apps the connection waits for', () => {
    setNamespace(s3({ state: 'pending' }))
    render(<ContentStorageBanner />)
    expect(screen.getByRole('status').textContent).toContain('eapps, content and seaweedfs are running')
  })

  // A workspace that names no provider has no app id to quote: the title says
  // so plainly instead of a stand-in like "(S3)".
  it('words a problem with no provider named without a placeholder', () => {
    setNamespace(s3({ providerApp: '', problem: 'NO_PROVIDER_CONFIG' }))
    render(<ContentStorageBanner />)
    const alert = screen.getByRole('alert')
    expect(alert.textContent).toContain('ecos-content is not wired to an S3 storage')
    expect(alert.textContent).not.toContain('(S3)')
    expect(alert.textContent).toContain('contentStorage.s3')
  })

  it('words a failed connection with no provider named without a placeholder', () => {
    setNamespace(s3({ providerApp: '', state: 'error', error: 'boom' }))
    render(<ContentStorageBanner />)
    const alert = screen.getByRole('alert')
    expect(alert.textContent).toContain('ecos-content could not be connected to its S3 storage')
    expect(alert.textContent).not.toContain('(S3)')
  })

  it('shows no progress when there is no provider to connect', () => {
    setNamespace(s3({ providerApp: '', state: 'pending' }))
    const { container } = render(<ContentStorageBanner />)
    expect(container.firstChild).toBeNull()
  })

  it('shows a generation problem even on a stopped namespace', () => {
    setNamespace(s3({ state: 'pending', problem: 'NO_PROVIDER_CONFIG' }), 'STOPPED')
    render(<ContentStorageBanner />)
    expect(screen.getByRole('alert').textContent).toContain('contentStorage.s3')
  })
})
