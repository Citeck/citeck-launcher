import { render, screen, waitFor } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useUpdateStore } from '../lib/updateStore'
import { UpdateDialog } from './UpdateDialog'

vi.mock('../lib/api', () => ({
  getUpdateChangelog: vi.fn().mockResolvedValue([
    { version: '2.5.0', date: '2026-03-01', markdown: '- a changelog entry' },
  ]),
  applyUpdate: vi.fn().mockResolvedValue({ applying: true, version: '2.5.0' }),
  getUpdateStatus: vi.fn(),
  checkUpdate: vi.fn(),
}))

describe('UpdateDialog', () => {
  beforeEach(() => {
    // jsdom does not implement <dialog> showModal/close — stub them.
    HTMLDialogElement.prototype.showModal = vi.fn()
    HTMLDialogElement.prototype.close = vi.fn()
    useUpdateStore.setState({
      status: { currentVersion: '2.4.0', latestVersion: '2.5.0', available: true, applying: false },
    })
  })

  it('loads and renders the changelog when open', async () => {
    render(<UpdateDialog open onClose={() => {}} />)
    await waitFor(() => expect(screen.getByText('a changelog entry')).toBeInTheDocument())
    // Version label from the release-note header (unique — not duplicated in body).
    expect(screen.getByText('2.5.0')).toBeInTheDocument()
  })
})
