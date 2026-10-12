import { render, screen, act, waitFor } from '@testing-library/react'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ContentStorageBanner } from './ContentStorageBanner'
import { useDashboardStore } from '../lib/store'
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

  it.each(['pending', 'waiting', 'provisioning', 'connected', 'admin-managed'])(
    'keeps normal %s state quiet on running and stopped namespaces',
    (state) => {
      for (const status of ['RUNNING', 'STOPPED'] as const) {
        setNamespace(s3({ state }), status)
        const { container, unmount } = render(<ContentStorageBanner />)
        expect(container.firstChild).toBeNull()
        unmount()
      }
    },
  )

  // A failed connection is broken right now: it is not dismissible, even when
  // normal progress that preceded it was silent, and it carries Retry.
  it('reports a failed connection with the daemon’s reason and a Retry that asks the daemon', async () => {
    setNamespace(s3({ state: 'provisioning' }))
    const { rerender } = render(<ContentStorageBanner />)
    expect(screen.queryByRole('status')).toBeNull()

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
