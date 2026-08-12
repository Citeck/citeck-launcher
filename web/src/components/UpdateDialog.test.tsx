import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useUpdateStore } from '../lib/updateStore'
import { UpdateDialog } from './UpdateDialog'
import { openExternal, getUpdateChangelog } from '../lib/api'

vi.mock('../lib/api', () => ({
  getUpdateChangelog: vi.fn().mockResolvedValue([
    { version: '2.5.0', date: '2026-03-01', markdown: '- a changelog entry' },
  ]),
  applyUpdate: vi.fn().mockResolvedValue({ applying: true, version: '2.5.0' }),
  getUpdateStatus: vi.fn(),
  checkUpdate: vi.fn(),
  openExternal: vi.fn().mockResolvedValue(undefined),
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

  it('shows the rollback banner and hides Install when the last update failed', async () => {
    useUpdateStore.setState({
      // No latestVersion → the failed version 2.6.0 appears only in the banner.
      status: { currentVersion: '2.4.0', available: false, applyError: '2.6.0', applying: false },
    })
    const { container } = render(<UpdateDialog open onClose={() => {}} />)
    await waitFor(() => expect(screen.getByText(/2\.6\.0/)).toBeInTheDocument())
    // Install (the primary button) is gated on `available` → absent after rollback.
    expect(container.querySelector('.bg-primary')).toBeNull()
  })

  it('shows the calm manual-update notice instead of Install when manualUpdateRequired', async () => {
    const releasesUrl = 'https://github.com/Citeck/citeck-launcher/releases'
    useUpdateStore.setState({
      status: {
        currentVersion: '2.4.0',
        latestVersion: '2.6.0',
        available: true,
        applying: false,
        manualUpdateRequired: true,
        manualUpdateReason: 'signature_mismatch',
        releasesUrl,
      },
    })
    const { container } = render(<UpdateDialog open onClose={() => {}} />)

    // Calm notice (info-styled, no destructive coloring) + releases button.
    await waitFor(() =>
      expect(screen.getByText(/download the new version from GitHub/)).toBeInTheDocument(),
    )
    expect(container.querySelector('.text-destructive')).toBeNull()
    // The auto-install action is hidden — it would just fail again.
    expect(screen.queryByText('Update & restart')).toBeNull()
    // The changelog ("what's new") still renders so the user sees what they're missing.
    expect(screen.getByText('a changelog entry')).toBeInTheDocument()

    // The releases button opens the URL from the status DTO (system browser
    // in desktop mode via openExternal).
    fireEvent.click(screen.getByText('Open releases page'))
    expect(vi.mocked(openExternal)).toHaveBeenCalledWith(releasesUrl)
  })
})

// The daemon reports "no notes" both when a release genuinely has none and when
// changelog/index.json could not be fetched (a 404 there is deliberately not an
// error). Right after a release the second case is the likely one — and since
// the dialog only loads notes on the open transition, an empty result outlives
// the condition that caused it. Observed in the wild on 2.9.2: the dialog sat on
// "No changelog available" for over an hour while the file was fetchable.
describe('UpdateDialog empty-changelog recovery', () => {
  beforeEach(() => {
    HTMLDialogElement.prototype.showModal = vi.fn()
    HTMLDialogElement.prototype.close = vi.fn()
    useUpdateStore.setState({
      status: { currentVersion: '2.9.1', latestVersion: '2.9.2', available: true, applying: false },
    })
  })

  it('offers a retry on the empty state that actually re-fetches', async () => {
    vi.mocked(getUpdateChangelog)
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([{ version: '2.9.2', date: '2026-08-12', markdown: '- recovered entry' }])

    render(<UpdateDialog open onClose={() => {}} />)
    await waitFor(() => expect(screen.getByText('Retry')).toBeInTheDocument())

    fireEvent.click(screen.getByText('Retry'))
    await waitFor(() => expect(screen.getByText('recovered entry')).toBeInTheDocument())
  })

  it('re-fetches the changelog on "Check now", not just the release status', async () => {
    const check = vi.fn().mockResolvedValue(undefined)
    useUpdateStore.setState({ check })
    vi.mocked(getUpdateChangelog)
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([{ version: '2.9.2', date: '2026-08-12', markdown: '- appeared now' }])

    render(<UpdateDialog open onClose={() => {}} />)
    await waitFor(() => expect(screen.getByText('Retry')).toBeInTheDocument())

    fireEvent.click(screen.getByText('Check now'))
    await waitFor(() => expect(check).toHaveBeenCalled())
    // Checking alone used to leave the dead end in place: the notes are loaded
    // only by the on-open effect, so the button could not fix what it sat next to.
    await waitFor(() => expect(screen.getByText('appeared now')).toBeInTheDocument())
  })
})
