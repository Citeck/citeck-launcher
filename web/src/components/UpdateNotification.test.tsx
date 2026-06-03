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
    useUpdateStore.setState({ status: null })
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
})
