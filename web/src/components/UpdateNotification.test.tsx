import { render, screen } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useUpdateStore } from '../lib/updateStore'
import { UpdateNotification } from './UpdateNotification'

vi.mock('../lib/api', () => ({
  getUpdateStatus: vi.fn().mockResolvedValue(null),
  checkUpdate: vi.fn(),
  getUpdateChangelog: vi.fn().mockResolvedValue([]),
  applyUpdate: vi.fn(),
}))

describe('UpdateNotification', () => {
  beforeEach(() => {
    // Stub the mount-effect refresh to a no-op so it doesn't asynchronously
    // mutate the store after the test's synchronous assertions (which triggers
    // React's "not wrapped in act(...)" warning). These tests drive state via
    // setState directly; they don't exercise refresh.
    useUpdateStore.setState({ status: null, refresh: () => Promise.resolve() })
    HTMLDialogElement.prototype.showModal = vi.fn()
    HTMLDialogElement.prototype.close = vi.fn()
  })

  it('renders nothing when no update is available', () => {
    useUpdateStore.setState({
      status: { currentVersion: '2.4.0', available: false, applying: false },
    })
    const { container } = render(<UpdateNotification />)
    expect(container.querySelector('button')).toBeNull()
  })

  it('shows a button when an update is available', () => {
    useUpdateStore.setState({
      status: { currentVersion: '2.4.0', latestVersion: '2.5.0', available: true, applying: false },
    })
    render(<UpdateNotification />)
    expect(screen.getByRole('button')).toBeInTheDocument()
  })

  it('shows a red error badge when the last update rolled back', () => {
    useUpdateStore.setState({
      status: { currentVersion: '2.4.0', available: false, applyError: '2.6.0', applying: false },
    })
    const { container } = render(<UpdateNotification />)
    expect(screen.getByRole('button')).toBeInTheDocument()
    expect(container.querySelector('.bg-red-500')).not.toBeNull()
    expect(container.querySelector('.bg-emerald-500')).toBeNull()
  })
})
