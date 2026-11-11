import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MCPSettings } from './MCPSettings'
import { ApiError, approveMCPOAuth, denyMCPOAuth, revokeMCPOAuth, getMCPOAuthConnections, getMCPOAuthPending, configureMCP, createMCPToken, getMCPStatus, revokeMCPToken } from '../lib/api'
import { copyText } from '../lib/clipboard'
import { LOCALES, t, useI18nStore } from '../lib/i18n'
import type { MCPOAuthPending, MCPStatus } from '../lib/api'

vi.mock('../lib/api', () => ({ ApiError: class extends Error {}, approveMCPOAuth: vi.fn(), denyMCPOAuth: vi.fn(), revokeMCPOAuth: vi.fn(), getMCPOAuthPending: vi.fn(), getMCPOAuthConnections: vi.fn(), getMCPStatus: vi.fn(), configureMCP: vi.fn(), createMCPToken: vi.fn(), revokeMCPToken: vi.fn() }))
vi.mock('../lib/clipboard', () => ({ copyText: vi.fn() }))
const storageWarning = 'MCP storage outcome is unconfirmed; MCP is closed until restart'
const revocationWarning = 'Revocation was NOT saved. This connection is denied in this process; retry is pending. If Launcher exits before a successful save, repeat the revocation after restart.'
const status: MCPStatus = { enabled: false, port: 19380, listening: false, endpoint: 'http://127.0.0.1:19380/mcp', tokens: [] }
const token = { id: 'id', name: 'Pi', role: 'observer' as const, createdAt: '2026-01-01T00:00:00Z' }
const value = 'citeck_mcp_fixture_only_once'
beforeEach(() => {
  vi.resetAllMocks()
  useI18nStore.getState().setLocale('en', false)
  vi.mocked(getMCPOAuthPending).mockResolvedValue({ pending: [] })
  vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [] })
  vi.mocked(getMCPStatus).mockResolvedValue(status)
  vi.mocked(copyText).mockResolvedValue(true)
  HTMLDialogElement.prototype.showModal = vi.fn(function (this: HTMLDialogElement) { this.open = true })
  HTMLDialogElement.prototype.close = vi.fn(function (this: HTMLDialogElement) { this.open = false })
})
async function openCreate() {
  fireEvent.click(await screen.findByRole('button', { name: 'Create token' }))
  return screen.getByRole('dialog', { name: 'Create token' })
}
function submit(name = 'Pi') {
  const dialog = screen.getByRole('dialog', { name: 'Create token' })
  fireEvent.change(within(dialog).getByLabelText('Token name'), { target: { value: name } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'Create token' }))
}
describe('MCPSettings — daemon-global local management', () => {
  it('is compact and default off without a namespace; copies an inactive endpoint and has no Lima mode', async () => {
    render(<MCPSettings />)
    expect(await screen.findByText('Not listening')).toBeInTheDocument()
    expect(screen.getByLabelText('Enable MCP')).not.toBeChecked()
    expect(screen.queryByLabelText('Token name')).not.toBeInTheDocument()
    expect(screen.getByText('Advanced settings').closest('details')).not.toHaveAttribute('open')
    expect(screen.queryByText(/Lima|host\.lima/)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Copy endpoint' }))
    expect(await screen.findByText('Copied')).toBeInTheDocument()
    expect(copyText).toHaveBeenCalledWith(status.endpoint)
  })
  it('exposes a loading skeleton, initial error and working retry', async () => {
    vi.mocked(getMCPStatus).mockRejectedValueOnce(new Error('offline')).mockResolvedValue(status)
    render(<MCPSettings />)
    expect(screen.getByRole('status', { name: 'Loading MCP settings' })).toBeInTheDocument()
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load MCP settings. Error: offline')
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('Not listening')).toBeInTheDocument()
    expect(getMCPStatus).toHaveBeenCalledTimes(2)
  })
  it('creates a 30-day Observer by default without configuring MCP; clears the once-only value on acknowledgment', async () => {
    vi.mocked(createMCPToken).mockResolvedValue({ token, value })
    render(<MCPSettings />)
    const dialog = await openCreate()
    expect(within(dialog).getByLabelText('Token name')).toHaveFocus()
    expect(within(dialog).getByRole('radio', { name: /Observer/ })).toBeChecked()
    expect(within(dialog).getByLabelText('Validity (days)')).toHaveValue(30)
    const before = Date.now()
    submit()
    expect(await screen.findByTestId('mcp-token-value')).toHaveTextContent(value)
    expect(screen.getByRole('button', { name: 'Copy token' })).toHaveFocus()
    const args = vi.mocked(createMCPToken).mock.calls[0]
    expect(args.slice(0, 3)).toEqual(['Pi', 'observer', false])
    expect(Date.parse(args[3]!)).toBeGreaterThanOrEqual(before + 30 * 86400000)
    expect(Date.parse(args[3]!)).toBeLessThanOrEqual(Date.now() + 30 * 86400000)
    expect(configureMCP).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Copy token' }))
    await waitFor(() => expect(copyText).toHaveBeenCalledWith(value, screen.getByRole('dialog', { name: 'Token created' })))
    fireEvent.click(screen.getByRole('button', { name: 'I saved the token' }))
    expect(screen.queryByText(value)).not.toBeInTheDocument()
    await openCreate()
    expect(screen.queryByTestId('mcp-token-value')).not.toBeInTheDocument()
  })
  it('retains a created value and metadata when the metadata refetch fails, never offering duplicate create', async () => {
    vi.mocked(createMCPToken).mockResolvedValue({ token, value })
    vi.mocked(getMCPStatus).mockResolvedValueOnce(status).mockRejectedValueOnce(new Error('offline'))
    render(<MCPSettings />)
    await openCreate(); submit()
    expect(await screen.findByTestId('mcp-token-value')).toHaveTextContent(value)
    expect(await screen.findByText(/list could not be refreshed/)).toBeInTheDocument()
    const dialog = screen.getByRole('dialog', { name: 'Token created' })
    expect(within(dialog).queryByRole('button', { name: 'Create token' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'I saved the token' }))
    expect(screen.getByText('Pi')).toBeInTheDocument()
    expect(createMCPToken).toHaveBeenCalledTimes(1)
  })
  it('blocks duplicate submissions, Close, Cancel and native Escape cancellation while create is unresolved', async () => {
    let resolve!: (result: { token: typeof token; value: string }) => void
    vi.mocked(createMCPToken).mockReturnValue(new Promise((r) => { resolve = r }))
    render(<MCPSettings />)
    const dialog = await openCreate(); submit()
    fireEvent.submit(dialog.querySelector('form')!)
    expect(createMCPToken).toHaveBeenCalledTimes(1)
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toBeDisabled()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }))
    const cancel = new Event('cancel', { cancelable: true, bubbles: false })
    fireEvent(dialog, cancel)
    expect(cancel.defaultPrevented).toBe(true)
    expect(dialog).toHaveAttribute('open')
    await act(async () => { resolve({ token, value }) })
    expect(screen.getByTestId('mcp-token-value')).toHaveTextContent(value)
  })
  it('clears the one-time value on close, not just acknowledgment', async () => {
    vi.mocked(createMCPToken).mockResolvedValue({ token, value })
    render(<MCPSettings />)
    await openCreate(); submit()
    await screen.findByTestId('mcp-token-value')
    await waitFor(() => expect(screen.getByRole('button', { name: 'I saved the token' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))
    await openCreate()
    expect(screen.queryByText(value)).not.toBeInTheDocument()
  })
  it('shows contextual Developer authority and explicitly chooses never-expiring; failure preserves the form', async () => {
    vi.mocked(createMCPToken).mockRejectedValue(new Error('disk full'))
    render(<MCPSettings />)
    const dialog = await openCreate()
    expect(within(dialog).queryByRole('note')).not.toBeInTheDocument()
    fireEvent.click(within(dialog).getByRole('radio', { name: /Developer/ }))
    const warning = screen.getByRole('note')
    for (const authority of ['images', 'commands', 'mounts', 'public ports', 'mounted scripts', 'execute code', 'expose host resources']) {
      expect(warning).toHaveTextContent(authority)
    }
    fireEvent.click(screen.getByLabelText('Never expires'))
    expect(screen.getByLabelText('Validity (days)')).toBeDisabled()
    submit()
    expect(await screen.findByRole('alert')).toHaveTextContent('disk full')
    expect(createMCPToken).toHaveBeenCalledWith('Pi', 'developer', true, undefined)
    expect(screen.getByLabelText('Token name')).toHaveValue('Pi')
    expect(screen.queryByTestId('mcp-token-value')).not.toBeInTheDocument()
  })
  it('bounds manual-token validity like OAuth approval while allowing explicit never expires', async () => {
    render(<MCPSettings />)
    const dialog = await openCreate()
    const days = within(dialog).getByLabelText('Validity (days)')
    fireEvent.change(days, { target: { value: '100000' } })
    expect(days).toBeInvalid()
    submit()
    expect(createMCPToken).not.toHaveBeenCalled()
    fireEvent.change(days, { target: { value: '36500' } })
    expect(days).toBeValid()
    fireEvent.click(within(dialog).getByLabelText('Never expires'))
    expect(days).toBeDisabled()
  })

  it('uses the chosen custom expiry', async () => {
    vi.mocked(createMCPToken).mockResolvedValue({ token, value })
    render(<MCPSettings />)
    await openCreate()
    fireEvent.change(screen.getByLabelText('Validity (days)'), { target: { value: '7' } })
    submit()
    await screen.findByTestId('mcp-token-value')
    expect(Date.parse(vi.mocked(createMCPToken).mock.calls[0][3]!)).toBeLessThanOrEqual(Date.now() + 7 * 86400000)
    expect(Date.parse(vi.mocked(createMCPToken).mock.calls[0][3]!)).toBeGreaterThan(Date.now() + 6.99 * 86400000)
  })
  it('does not claim successful copying when the shared clipboard helper fails', async () => {
    vi.mocked(copyText).mockResolvedValue(false)
    render(<MCPSettings />)
    fireEvent.click(await screen.findByRole('button', { name: 'Copy endpoint' }))
    await waitFor(() => expect(copyText).toHaveBeenCalled())
    expect(screen.queryByText('Copied')).not.toBeInTheDocument()
  })
  it('retains the one-time value and does not claim copied when token copying fails', async () => {
    vi.mocked(createMCPToken).mockResolvedValue({ token, value })
    vi.mocked(copyText).mockResolvedValue(false)
    render(<MCPSettings />)
    await openCreate(); submit()
    await screen.findByTestId('mcp-token-value')
    fireEvent.click(screen.getByRole('button', { name: 'Copy token' }))
    await waitFor(() => expect(copyText).toHaveBeenCalledWith(value, screen.getByRole('dialog', { name: 'Token created' })))
    expect(screen.queryByText('Copied')).not.toBeInTheDocument()
    expect(screen.getByTestId('mcp-token-value')).toHaveTextContent(value)
  })
  it('saves explicit enable/port and keeps actual state truthful after a failed request, then retries', async () => {
    vi.mocked(configureMCP).mockRejectedValueOnce(new Error('port unavailable')).mockResolvedValue({ ...status, enabled: true, listening: true, port: 19400, endpoint: 'http://127.0.0.1:19400/mcp' })
    render(<MCPSettings />)
    await screen.findByText('Not listening')
    fireEvent.click(screen.getByLabelText('Enable MCP'))
    fireEvent.click(screen.getByText('Advanced settings'))
    fireEvent.change(screen.getByLabelText('Loopback port'), { target: { value: '19400' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save MCP settings' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('port unavailable')
    expect(configureMCP).toHaveBeenCalledWith(true, 19400)
    expect(screen.queryByText('Listening')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Save MCP settings' }))
    expect(await screen.findByText('Listening')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
  it('distinguishes enabled desired state from a returned bind failure', async () => {
    vi.mocked(getMCPStatus).mockResolvedValue({ ...status, enabled: true, error: 'bind failed' })
    render(<MCPSettings />)
    expect(await screen.findByText('Connection error')).toBeInTheDocument()
    expect(screen.getByLabelText('Enable MCP')).toBeChecked()
    expect(screen.getByRole('alert')).toHaveTextContent('bind failed')
    expect(screen.queryByText('Listening')).not.toBeInTheDocument()
  })
  it('blocks repeated save and revoke while pending and reports revoke failure without hiding metadata', async () => {
    let reject!: (error: Error) => void
    vi.mocked(getMCPStatus).mockResolvedValue({ ...status, tokens: [token] })
    vi.mocked(revokeMCPToken).mockReturnValue(new Promise((_resolve, r) => { reject = r }))
    render(<MCPSettings />)
    const revoke = await screen.findByRole('button', { name: 'Revoke Pi' })
    fireEvent.click(revoke); fireEvent.click(revoke)
    expect(revokeMCPToken).toHaveBeenCalledTimes(1)
    expect(revoke).toBeDisabled()
    await act(async () => { reject(new Error('save failed')) })
    expect(screen.getByRole('alert')).toHaveTextContent('save failed')
    expect(screen.getByText('Pi')).toBeInTheDocument()
    vi.mocked(configureMCP).mockReturnValue(new Promise(() => {}))
    const save = screen.getByRole('button', { name: 'Save MCP settings' })
    fireEvent.click(save); fireEvent.click(save)
    expect(configureMCP).toHaveBeenCalledTimes(1)
    expect(save).toBeDisabled()
    expect(revoke).toBeDisabled()
  })
  it('blocks configuration during deferred revocation, including direct form submit, so stale metadata cannot restore the token', async () => {
    const revoked = { ...status, tokens: [{ ...token, revokedAt: '2026-01-02T00:00:00Z' }] }
    let finishRevoke!: (result: MCPStatus) => void
    vi.mocked(getMCPStatus).mockResolvedValue({ ...status, tokens: [token] })
    vi.mocked(revokeMCPToken).mockReturnValue(new Promise((resolve) => { finishRevoke = resolve }))
    // If invoked before revocation finishes, this would capture stale metadata.
    vi.mocked(configureMCP).mockImplementation(async () => ({ ...status, tokens: [token] }))
    render(<MCPSettings />)
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Pi' }))
    const save = screen.getByRole('button', { name: 'Save MCP settings' })
    expect(save).toBeDisabled()
    fireEvent.click(save)
    fireEvent.submit(save.closest('form')!)
    expect(configureMCP).not.toHaveBeenCalled()
    await act(async () => { finishRevoke(revoked) })
    expect(screen.getByText('Revoked')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Revoke Pi' })).not.toBeInTheDocument()
    expect(save).toBeEnabled()
    expect(configureMCP).not.toHaveBeenCalled()
    // Once revocation has settled, a new configuration request sees its result.
    vi.mocked(configureMCP).mockResolvedValue({ ...revoked, enabled: true, listening: true })
    fireEvent.click(save)
    await screen.findByText('Listening')
    expect(screen.getByText('Revoked')).toBeInTheDocument()
  })
  it('also blocks revocation while configuration is pending, including same-turn events before rerender', async () => {
    let finishSave!: (result: MCPStatus) => void
    vi.mocked(getMCPStatus).mockResolvedValue({ ...status, tokens: [token] })
    vi.mocked(configureMCP).mockReturnValue(new Promise((resolve) => { finishSave = resolve }))
    vi.mocked(revokeMCPToken).mockResolvedValue({ ...status, tokens: [{ ...token, revokedAt: '2026-01-02T00:00:00Z' }] })
    render(<MCPSettings />)
    const revoke = await screen.findByRole('button', { name: 'Revoke Pi' })
    const save = screen.getByRole('button', { name: 'Save MCP settings' })
    act(() => {
      fireEvent.submit(save.closest('form')!)
      fireEvent.click(revoke)
    })
    expect(configureMCP).toHaveBeenCalledTimes(1)
    expect(revokeMCPToken).not.toHaveBeenCalled()
    expect(revoke).toBeDisabled()
    await act(async () => { finishSave({ ...status, tokens: [token] }) })
    expect(revoke).toBeEnabled()
    fireEvent.click(revoke)
    expect(await screen.findByText('Revoked')).toBeInTheDocument()
  })
  it('revokes metadata-only tokens, and distinguishes expired and revoked rows', async () => {
    const expired = { ...token, id: 'expired', name: 'Old', expiresAt: '2000-01-01T00:00:00Z' }
    vi.mocked(getMCPStatus).mockResolvedValue({ ...status, tokens: [token, expired] })
    vi.mocked(revokeMCPToken).mockResolvedValue({ ...status, tokens: [{ ...token, revokedAt: '2026-01-02T00:00:00Z' }, expired] })
    render(<MCPSettings />)
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Pi' }))
    expect(await screen.findByText('Revoked')).toBeInTheDocument()
    expect(screen.getByText('Expired')).toBeInTheDocument()
    expect(revokeMCPToken).toHaveBeenCalledWith('id')
    expect(screen.queryByTestId('mcp-token-value')).not.toBeInTheDocument()
  })
})

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

describe('MCPSettings — last used and explicit metadata refresh', () => {
  it('renders local fixed-format times for active, revoked and expired tokens, and No data for missing/invalid timestamps', async () => {
    const lastUsedAt = '2026-01-15T12:34:56Z'
    vi.mocked(getMCPStatus).mockResolvedValue({ ...status, tokens: [
      { ...token, lastUsedAt },
      { ...token, id: 'revoked', name: 'Revoked client', lastUsedAt, revokedAt: '2026-01-16T00:00:00Z' },
      { ...token, id: 'expired', name: 'Expired client', lastUsedAt, expiresAt: '2000-01-01T00:00:00Z' },
      { ...token, id: 'missing', name: 'Missing timestamp' },
      { ...token, id: 'invalid', name: 'Invalid timestamp', lastUsedAt: 'invalid' },
    ] })
    const { container } = render(<MCPSettings />)
    await screen.findByText('Pi')
    const times = container.querySelectorAll('time')
    expect(times).toHaveLength(3)
    // Run this suite with TZ=America/New_York too: UTC must not leak into display.
    const hour = String(new Date(lastUsedAt).getHours()).padStart(2, '0')
    times.forEach((time) => {
      expect(time).toHaveAttribute('datetime', '2026-01-15T12:34:56.000Z')
      expect(time).toHaveTextContent(`15.01.26 ${hour}:34:56`)
    })
    expect(screen.getAllByText('Last used: No data')).toHaveLength(2)
    expect(screen.getByText('Revoked')).toBeInTheDocument()
    expect(screen.getByText('Expired')).toBeInTheDocument()
    expect(screen.getByText(/Last successful authentication, not tool completion/)).toBeInTheDocument()
  })

  it('refreshes metadata without resetting unsaved inputs or configuring/enabling MCP', async () => {
    vi.mocked(getMCPStatus).mockResolvedValueOnce({ ...status, tokens: [token] }).mockResolvedValueOnce({
      ...status, tokens: [{ ...token, lastUsedAt: '2026-01-15T12:34:56Z' }],
    })
    const { container } = render(<MCPSettings />)
    await screen.findByText('Pi')
    fireEvent.click(screen.getByLabelText('Enable MCP'))
    fireEvent.change(screen.getByLabelText('Loopback port'), { target: { value: '19400' } })
    fireEvent.click(screen.getByRole('button', { name: 'Refresh metadata' }))
    await waitFor(() => expect(container.querySelector('time')).not.toBeNull())
    expect(screen.getByLabelText('Enable MCP')).toBeChecked()
    expect(screen.getByLabelText('Loopback port')).toHaveValue(19400)
    expect(screen.getByText('Not listening')).toBeInTheDocument()
    expect(configureMCP).not.toHaveBeenCalled()
    expect(getMCPStatus).toHaveBeenCalledTimes(2)
  })

  it('keeps old metadata on refresh failure and allows retry', async () => {
    vi.mocked(getMCPStatus).mockResolvedValueOnce({ ...status, tokens: [token] })
      .mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ ...status, tokens: [{ ...token, revokedAt: '2026-01-16T00:00:00Z' }] })
    render(<MCPSettings />)
    const refresh = await screen.findByRole('button', { name: 'Refresh metadata' })
    fireEvent.click(refresh)
    expect(await screen.findByRole('alert')).toHaveTextContent('Previous data is shown. Try again. Error: offline')
    expect(screen.getByRole('button', { name: 'Revoke Pi' })).toBeEnabled()
    fireEvent.click(refresh)
    expect(await screen.findByText('Revoked')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(getMCPStatus).toHaveBeenCalledTimes(3)
  })

  it('serializes deferred refresh against repeated refresh, save, revoke and opening create, even in the same turn', async () => {
    const pending = deferred<MCPStatus>()
    vi.mocked(getMCPStatus).mockResolvedValueOnce({ ...status, tokens: [token] }).mockReturnValueOnce(pending.promise)
    vi.mocked(revokeMCPToken).mockResolvedValue({ ...status, tokens: [{ ...token, revokedAt: '2026-01-16T00:00:00Z' }] })
    render(<MCPSettings />)
    const refresh = await screen.findByRole('button', { name: 'Refresh metadata' })
    const save = screen.getByRole('button', { name: 'Save MCP settings' })
    const revoke = screen.getByRole('button', { name: 'Revoke Pi' })
    const create = screen.getByRole('button', { name: 'Create token' })
    act(() => {
      fireEvent.click(refresh); fireEvent.click(refresh)
      fireEvent.submit(save.closest('form')!); fireEvent.click(revoke); fireEvent.click(create)
    })
    expect(getMCPStatus).toHaveBeenCalledTimes(2)
    expect(configureMCP).not.toHaveBeenCalled()
    expect(revokeMCPToken).not.toHaveBeenCalled()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    for (const button of [refresh, save, revoke, create]) expect(button).toBeDisabled()
    await act(async () => pending.resolve({ ...status, tokens: [token] }))
    fireEvent.click(revoke)
    expect(await screen.findByText('Revoked')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Revoke Pi' })).not.toBeInTheDocument()
  })

  it.each(['save', 'revoke'] as const)('does not start a refresh while %s is in flight', async (operation) => {
    const pending = deferred<MCPStatus>()
    vi.mocked(getMCPStatus).mockResolvedValue({ ...status, tokens: [token] })
    vi.mocked(operation === 'save' ? configureMCP : revokeMCPToken).mockReturnValue(pending.promise)
    render(<MCPSettings />)
    const refresh = await screen.findByRole('button', { name: 'Refresh metadata' })
    act(() => {
      fireEvent.click(screen.getByRole('button', { name: operation === 'save' ? 'Save MCP settings' : 'Revoke Pi' }))
      fireEvent.click(refresh)
    })
    expect(getMCPStatus).toHaveBeenCalledTimes(1)
    expect(refresh).toBeDisabled()
    await act(async () => pending.resolve({ ...status, tokens: [{ ...token, revokedAt: '2026-01-16T00:00:00Z' }] }))
    expect(screen.getByText('Revoked')).toBeInTheDocument()
    expect(refresh).toBeEnabled()
  })

  it('holds the shared guard throughout create and its secondary GET, preserving the one-time value', async () => {
    const creation = deferred<{ token: typeof token; value: string }>()
    const metadata = deferred<MCPStatus>()
    vi.mocked(createMCPToken).mockReturnValue(creation.promise)
    vi.mocked(getMCPStatus).mockResolvedValueOnce(status).mockReturnValueOnce(metadata.promise)
    render(<MCPSettings />)
    const refresh = await screen.findByRole('button', { name: 'Refresh metadata' })
    const save = screen.getByRole('button', { name: 'Save MCP settings' })
    await openCreate(); submit()
    fireEvent.click(refresh); fireEvent.submit(save.closest('form')!)
    expect(getMCPStatus).toHaveBeenCalledTimes(1)
    expect(configureMCP).not.toHaveBeenCalled()
    await act(async () => creation.resolve({ token, value }))
    expect(screen.getByTestId('mcp-token-value')).toHaveTextContent(value)
    expect(refresh).toBeDisabled()
    const dialog = screen.getByRole('dialog', { name: 'Token created' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }))
    const cancel = new Event('cancel', { cancelable: true })
    fireEvent(dialog, cancel)
    expect(cancel.defaultPrevented).toBe(true)
    expect(screen.getByRole('button', { name: 'I saved the token' })).toBeDisabled()
    await act(async () => metadata.resolve({ ...status, tokens: [token] }))
    expect(screen.getByTestId('mcp-token-value')).toHaveTextContent(value)
    expect(refresh).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'I saved the token' }))
    expect(refresh).toBeEnabled()
    expect(screen.getByLabelText('Enable MCP')).not.toBeChecked()
    expect(configureMCP).not.toHaveBeenCalled()
  })

  it('releases the modal guard on cancel without fetching or mutating', async () => {
    render(<MCPSettings />)
    const dialog = await openCreate()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(screen.getByRole('button', { name: 'Refresh metadata' })).toBeEnabled()
    expect(getMCPStatus).toHaveBeenCalledTimes(1)
    expect(createMCPToken).not.toHaveBeenCalled()
  })

  it('ignores an initial response after unmount; a fresh mount gets its own state', async () => {
    const initial = deferred<MCPStatus>()
    vi.mocked(getMCPStatus).mockReturnValueOnce(initial.promise).mockResolvedValueOnce(status)
    const view = render(<MCPSettings />)
    view.unmount()
    render(<MCPSettings />)
    await screen.findByText('Not listening')
    await act(async () => initial.resolve({ ...status, enabled: true, listening: true, tokens: [token] }))
    expect(screen.queryByText('Pi')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Enable MCP')).not.toBeChecked()
  })

  it.each(['refresh', 'save', 'revoke'] as const)('ignores a deferred %s result after unmount', async (operation) => {
    const pending = deferred<MCPStatus>()
    vi.mocked(getMCPStatus).mockResolvedValueOnce({ ...status, tokens: [token] })
    if (operation === 'refresh') vi.mocked(getMCPStatus).mockReturnValueOnce(pending.promise)
    else vi.mocked(operation === 'save' ? configureMCP : revokeMCPToken).mockReturnValueOnce(pending.promise)
    const view = render(<MCPSettings />)
    await screen.findByText('Pi')
    fireEvent.click(screen.getByRole('button', { name: operation === 'refresh' ? 'Refresh metadata' : operation === 'save' ? 'Save MCP settings' : 'Revoke Pi' }))
    view.unmount()
    await act(async () => pending.resolve({ ...status, enabled: true, listening: true, tokens: [token] }))
    expect(screen.queryByRole('region')).not.toBeInTheDocument()
    expect(getMCPStatus).toHaveBeenCalledTimes(operation === 'refresh' ? 2 : 1)
  })

  it('does not start the secondary metadata GET when creation resolves after unmount', async () => {
    const pending = deferred<{ token: typeof token; value: string }>()
    vi.mocked(createMCPToken).mockReturnValueOnce(pending.promise)
    const view = render(<MCPSettings />)
    await openCreate(); submit(); view.unmount()
    await act(async () => pending.resolve({ token, value }))
    expect(getMCPStatus).toHaveBeenCalledTimes(1)
    expect(screen.queryByText(value)).not.toBeInTheDocument()
  })

  it('ignores the secondary metadata response after unmount', async () => {
    const pending = deferred<MCPStatus>()
    vi.mocked(createMCPToken).mockResolvedValue({ token, value })
    vi.mocked(getMCPStatus).mockResolvedValueOnce(status).mockReturnValueOnce(pending.promise)
    const view = render(<MCPSettings />)
    await openCreate(); submit()
    await screen.findByTestId('mcp-token-value')
    view.unmount()
    await act(async () => pending.resolve({ ...status, tokens: [token] }))
    expect(screen.queryByText('Pi')).not.toBeInTheDocument()
    expect(getMCPStatus).toHaveBeenCalledTimes(2)
  })

  it('uses the expiry timer only for local expiry, never metadata polling, and clears it on unmount', async () => {
    vi.useFakeTimers()
    try {
      vi.mocked(getMCPStatus).mockResolvedValue({ ...status, tokens: [{ ...token, expiresAt: new Date(Date.now() + 10000).toISOString() }] })
      const view = render(<MCPSettings />)
      await act(async () => {})
      expect(screen.queryByText('Expired')).not.toBeInTheDocument()
      await act(async () => vi.advanceTimersByTime(30000))
      expect(screen.getByText('Expired')).toBeInTheDocument()
      expect(getMCPStatus).toHaveBeenCalledTimes(1)
      view.unmount()
      expect(vi.getTimerCount()).toBe(0)
    } finally { vi.useRealTimers() }
  })
})


const pendingRequest: MCPOAuthPending = { id: 'pending', clientId: 'client', clientName: 'Pi <unverified>', correlationCode: 'AB12CD34', status: 'pending', createdAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 300000).toISOString() }
const connection = { revoked: false, id: 'grant', clientId: 'client', clientName: 'Connected Pi', role: 'observer' as const, approvedAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 86400000).toISOString() }
function oauthFixture() {
  vi.mocked(getMCPStatus).mockResolvedValue({ ...status, enabled: true, listening: true })
  vi.mocked(getMCPOAuthPending).mockResolvedValue({ pending: [pendingRequest] })
  vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [connection] })
}

describe('MCPSettings — owner OAuth decisions and connections', () => {
  it('shows existing connections as revoked after a saved port change without pending approvals', async () => {
    oauthFixture()
    vi.mocked(getMCPOAuthPending).mockResolvedValue({ pending: [] })
    render(<MCPSettings />)
    await screen.findByRole('button', { name: 'Revoke Connected Pi' })
    vi.mocked(configureMCP).mockResolvedValue({ ...status, enabled: true, listening: true, port: 19444, endpoint: 'http://127.0.0.1:19444/mcp' })
    vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [{ ...connection, revoked: true }] })
    fireEvent.change(screen.getByLabelText('Loopback port'), { target: { value: '19444' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save MCP settings' }))
    expect(await screen.findByText('Revoked')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Revoke Connected Pi' })).not.toBeInTheDocument()
    expect(configureMCP).toHaveBeenCalledExactlyOnceWith(true, 19444)
    expect(getMCPOAuthConnections).toHaveBeenCalledTimes(2)
  })

  it('keeps a saved port change truthful if the secondary connection refresh fails', async () => {
    oauthFixture()
    render(<MCPSettings />)
    await screen.findByText('Connected Pi')
    vi.mocked(configureMCP).mockResolvedValue({ ...status, enabled: true, listening: true, port: 19444, endpoint: 'http://127.0.0.1:19444/mcp' })
    vi.mocked(getMCPOAuthConnections).mockRejectedValueOnce(new Error('temporary metadata failure'))
    fireEvent.change(screen.getByLabelText('Loopback port'), { target: { value: '19444' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save MCP settings' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Previous data is shown')
    expect(screen.getByText('http://127.0.0.1:19444/mcp')).toBeInTheDocument()
    expect(screen.getByText('Connected Pi')).toBeInTheDocument()
    expect(configureMCP).toHaveBeenCalledTimes(1)
    vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [{ ...connection, revoked: true }] })
    vi.mocked(getMCPStatus).mockResolvedValue({ ...status, enabled: true, port: 19444 })
    fireEvent.click(screen.getByRole('button', { name: 'Refresh metadata' }))
    expect(await screen.findByText('Revoked')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(configureMCP).toHaveBeenCalledTimes(1)
  })

  it('clears a loaded unsaved-revocation warning after a successful revoke retry', async () => {
    oauthFixture()
    vi.mocked(getMCPStatus).mockResolvedValue({ ...status, enabled: true, oauthWarning: revocationWarning })
    vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [connection], warning: revocationWarning })
    render(<MCPSettings />)
    expect(await screen.findByRole('alert')).toHaveTextContent('may return after restart')
    vi.mocked(revokeMCPOAuth).mockResolvedValue({ status: 'revoked' })
    vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [{ ...connection, revoked: true }] })
    fireEvent.click(screen.getByRole('button', { name: 'Revoke Connected Pi' }))
    await screen.findByText('Revoked')
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
    expect(getMCPStatus).toHaveBeenCalledTimes(1)
  })

  it('refreshes connections after browser completion, retries failed reads, then stops', async () => {
    vi.useFakeTimers()
    try {
      oauthFixture()
      vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [] })
      vi.mocked(approveMCPOAuth).mockResolvedValue({ status: 'approved' })
      const view = render(<MCPSettings />)
      await act(async () => {})
      vi.mocked(getMCPOAuthPending).mockResolvedValue({ pending: [{ ...pendingRequest, status: 'approved' }] })
      fireEvent.click(screen.getByRole('button', { name: 'Allow access' }))
      await act(async () => {})
      expect(screen.queryByText('Connected Pi')).not.toBeInTheDocument()
      vi.mocked(getMCPOAuthPending).mockResolvedValue({ pending: [] })
      vi.mocked(getMCPOAuthConnections).mockRejectedValueOnce(new Error('temporary read failure'))
      await act(async () => vi.advanceTimersByTime(3000))
      vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [connection] })
      await act(async () => vi.advanceTimersByTime(3000))
      expect(screen.getByText('Connected Pi')).toBeInTheDocument()
      const reads = vi.mocked(getMCPOAuthConnections).mock.calls.length
      await act(async () => vi.advanceTimersByTime(9000))
      expect(getMCPOAuthConnections).toHaveBeenCalledTimes(reads)
      view.unmount()
      expect(vi.getTimerCount()).toBe(0)
    } finally { vi.useRealTimers() }
  })

  it('does not let a stale completion refresh undo a newer revoke', async () => {
    vi.useFakeTimers()
    try {
      oauthFixture()
      vi.mocked(getMCPOAuthPending).mockResolvedValue({ pending: [{ ...pendingRequest, status: 'approved' }] })
      const view = render(<MCPSettings />)
      await act(async () => {})
      const stale = deferred<{ connections: typeof connection[] }>()
      vi.mocked(getMCPOAuthPending).mockResolvedValue({ pending: [] })
      vi.mocked(getMCPOAuthConnections).mockReturnValueOnce(stale.promise)
      await act(async () => vi.advanceTimersByTime(3000))
      vi.mocked(revokeMCPOAuth).mockResolvedValue({ status: 'revoked' })
      vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [{ ...connection, revoked: true }] })
      fireEvent.click(screen.getByRole('button', { name: 'Revoke Connected Pi' }))
      await act(async () => {})
      await act(async () => stale.resolve({ connections: [connection] }))
      expect(screen.getByText('Revoked')).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Revoke Connected Pi' })).not.toBeInTheDocument()
      view.unmount()
    } finally { vi.useRealTimers() }
  })

  it('aborts the completion refresh when the settings page unmounts', async () => {
    vi.useFakeTimers()
    try {
      oauthFixture()
      vi.mocked(getMCPOAuthPending).mockResolvedValue({ pending: [{ ...pendingRequest, status: 'approved' }] })
      const view = render(<MCPSettings />)
      await act(async () => {})
      const stale = deferred<{ connections: typeof connection[] }>()
      vi.mocked(getMCPOAuthPending).mockResolvedValue({ pending: [] })
      vi.mocked(getMCPOAuthConnections).mockReturnValueOnce(stale.promise)
      await act(async () => vi.advanceTimersByTime(3000))
      const signal = vi.mocked(getMCPOAuthConnections).mock.calls[1][0]!
      view.unmount()
      expect(signal.aborted).toBe(true)
      await act(async () => stale.resolve({ connections: [connection] }))
      await act(async () => vi.advanceTimersByTime(9000))
      expect(getMCPOAuthConnections).toHaveBeenCalledTimes(2)
      expect(vi.getTimerCount()).toBe(0)
    } finally { vi.useRealTimers() }
  })

  it('sends the owner name and selected days, or explicit neverExpires', async () => {
    oauthFixture()
    vi.mocked(approveMCPOAuth).mockRejectedValue(new Error('retry'))
    render(<MCPSettings />)
    await screen.findByText('AB12CD34')
    fireEvent.change(screen.getByLabelText('Connection name'), { target: { value: 'Pi on Mac' } })
    fireEvent.change(screen.getByLabelText('Validity (days)'), { target: { value: '90' } })
    const before = Date.now()
    fireEvent.click(screen.getByRole('button', { name: 'Allow access' }))
    await screen.findByRole('alert')
    const options = vi.mocked(approveMCPOAuth).mock.calls[0][2]!
    expect(options.name).toBe('Pi on Mac')
    expect(options.neverExpires).toBe(false)
    expect(Date.parse(options.expiresAt!)).toBeGreaterThanOrEqual(before + 90 * 86400000)
    fireEvent.click(screen.getByLabelText('Never expires'))
    expect(screen.getByLabelText('Validity (days)')).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Allow access' }))
    await act(async () => {})
    expect(approveMCPOAuth).toHaveBeenLastCalledWith('pending', 'observer', { name: 'Pi on Mac', neverExpires: true })
  })

  it('shows an owner label and never expires without losing the original client name', async () => {
    oauthFixture()
    vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [{ ...connection, name: 'Pi on Mac', neverExpires: true, expiresAt: '' }] })
    render(<MCPSettings />)
    await screen.findByText('Pi on Mac')
    expect(screen.getByText(/Connected Pi.*Unverified name/)).toBeInTheDocument()
    expect(screen.getAllByText('Never expires').length).toBeGreaterThan(0)
    expect(screen.getByRole('button', { name: 'Revoke Pi on Mac' })).toBeInTheDocument()
  })

  it('shows an old revoked connection without a timestamp as revoked, not actionable', async () => {
    oauthFixture()
    vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [{ ...connection, revoked: true }] })
    render(<MCPSettings />)
    await screen.findByText('Connected Pi')
    expect(screen.getByText('Revoked')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Revoke Connected Pi' })).not.toBeInTheDocument()
  })

  it('compares unverified name/code and allows Observer by default without a duplicate decision', async () => {
    oauthFixture()
    const decision = deferred<{ status: string }>()
    vi.mocked(approveMCPOAuth).mockReturnValue(decision.promise)
    render(<MCPSettings />)
    expect(await screen.findByText('AB12CD34')).toBeInTheDocument()
    expect(screen.getByText('Pi <unverified>')).toHaveTextContent('Pi <unverified>')
    expect(screen.getByRole('radio', { name: 'Observer' })).toBeChecked()
    fireEvent.click(screen.getByRole('button', { name: 'Allow access' }))
    expect(screen.getByRole('button', { name: 'Allow access' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Create token' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Allow access' }))
    expect(approveMCPOAuth).toHaveBeenCalledExactlyOnceWith('pending', 'observer', { name: pendingRequest.clientName, neverExpires: false, expiresAt: expect.any(String) })
    vi.mocked(getMCPOAuthPending).mockResolvedValue({ pending: [{ ...pendingRequest, status: 'approved', role: 'observer' }] })
    await act(async () => decision.resolve({ status: 'approved' }))
    expect(await screen.findByText('Allowed. Waiting for the browser to finish sign-in.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Allow access' })).not.toBeInTheDocument()
  })

  it('requires an explicit Developer selection, shows its warning and can deny instead', async () => {
    oauthFixture()
    vi.mocked(denyMCPOAuth).mockResolvedValue({ status: 'denied' })
    render(<MCPSettings />)
    await screen.findByText('AB12CD34')
    fireEvent.click(screen.getByRole('radio', { name: 'Developer' }))
    expect(screen.getByRole('note')).toHaveTextContent('Only give Developer access to an agent you trust')
    expect(approveMCPOAuth).not.toHaveBeenCalled()
    vi.mocked(getMCPOAuthPending).mockResolvedValue({ pending: [] })
    fireEvent.click(screen.getByRole('button', { name: 'Deny' }))
    expect(await screen.findByText('No requests waiting for approval.')).toBeInTheDocument()
    expect(denyMCPOAuth).toHaveBeenCalledExactlyOnceWith('pending')
    expect(approveMCPOAuth).not.toHaveBeenCalled()
  })

  it('sends Developer only after Allow and reports an expired decision without inventing a connection', async () => {
    oauthFixture()
    vi.mocked(approveMCPOAuth).mockRejectedValue(new Error('request expired'))
    render(<MCPSettings />)
    await screen.findByText('AB12CD34')
    fireEvent.click(screen.getByRole('radio', { name: 'Developer' }))
    fireEvent.click(screen.getByRole('button', { name: 'Allow access' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('request expired')
    expect(approveMCPOAuth).toHaveBeenCalledExactlyOnceWith('pending', 'developer', { name: pendingRequest.clientName, neverExpires: false, expiresAt: expect.any(String) })
    expect(screen.getAllByText('Connected Pi')).toHaveLength(1)
    expect(screen.getByRole('button', { name: 'Allow access' })).toBeEnabled()
  })

  it('polls pending only while visible; aborts and discards an in-flight response on unmount', async () => {
    vi.useFakeTimers()
    try {
      oauthFixture()
      const stale = deferred<{ pending: MCPOAuthPending[] }>()
      vi.mocked(getMCPOAuthPending).mockReturnValueOnce(stale.promise)
      const view = render(<MCPSettings />)
      await act(async () => {})
      expect(getMCPOAuthPending).toHaveBeenCalledTimes(1)
      const signal = vi.mocked(getMCPOAuthPending).mock.calls[0][0]!
      view.unmount()
      expect(signal.aborted).toBe(true)
      await act(async () => stale.resolve({ pending: [pendingRequest] }))
      await act(async () => vi.advanceTimersByTime(10000))
      expect(getMCPOAuthPending).toHaveBeenCalledTimes(1)
      expect(getMCPOAuthConnections).toHaveBeenCalledTimes(1)
      expect(vi.getTimerCount()).toBe(0)
    } finally { vi.useRealTimers() }
  })

  it('skips hidden-page polls, removes expired requests and never polls Last used', async () => {
    vi.useFakeTimers()
    try {
      oauthFixture()
      vi.mocked(getMCPOAuthPending).mockResolvedValue({ pending: [{ ...pendingRequest, expiresAt: new Date(Date.now() + 1000).toISOString() }] })
      const view = render(<MCPSettings />)
      await act(async () => {})
      expect(screen.getByText('AB12CD34')).toBeInTheDocument()
      const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
      await act(async () => vi.advanceTimersByTime(30000))
      expect(getMCPOAuthPending).toHaveBeenCalledTimes(1)
      expect(getMCPOAuthConnections).toHaveBeenCalledTimes(1)
      expect(screen.queryByText('AB12CD34')).not.toBeInTheDocument()
      visibility.mockRestore()
      view.unmount()
    } finally { vi.useRealTimers() }
  })

  it('discards a stale pending poll after a decision and preserves a failed-refresh snapshot and unsaved settings', async () => {
    vi.useFakeTimers()
    try {
      oauthFixture()
      const stale = deferred<{ pending: MCPOAuthPending[] }>()
      const view = render(<MCPSettings />)
      await act(async () => {})
      vi.mocked(getMCPOAuthPending).mockReturnValueOnce(stale.promise)
      await act(async () => vi.advanceTimersByTime(3000))
      vi.mocked(denyMCPOAuth).mockResolvedValue({ status: 'denied' })
      vi.mocked(getMCPOAuthPending).mockResolvedValue({ pending: [] })
      fireEvent.click(screen.getByRole('button', { name: 'Deny' }))
      await act(async () => {})
      await act(async () => stale.resolve({ pending: [pendingRequest] }))
      expect(screen.queryByRole('button', { name: 'Allow access' })).not.toBeInTheDocument()
      fireEvent.click(screen.getByLabelText('Enable MCP'))
      fireEvent.change(screen.getByLabelText('Loopback port'), { target: { value: '19444' } })
      vi.mocked(getMCPOAuthConnections).mockRejectedValue(new Error('offline'))
      fireEvent.click(screen.getByRole('button', { name: 'Refresh metadata' }))
      await act(async () => {})
      expect(screen.getByRole('alert')).toHaveTextContent('Previous data is shown')
      expect(screen.getByText('Connected Pi')).toBeInTheDocument()
      expect(screen.getByLabelText('Enable MCP')).not.toBeChecked()
      expect(screen.getByLabelText('Loopback port')).toHaveValue(19444)
      view.unmount()
    } finally { vi.useRealTimers() }
  })

  it('does not report an unsaved revoke as durable success', async () => {
    oauthFixture()
    vi.mocked(revokeMCPOAuth).mockRejectedValue(new ApiError('MCP_OAUTH_REVOCATION_UNCONFIRMED: ' + revocationWarning, 503, ''))
    render(<MCPSettings />)
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Connected Pi' }))
    await waitFor(() => expect(screen.getAllByRole('alert').some((a) => a.textContent?.includes('may return after restart'))).toBe(true))
    expect(screen.getByRole('button', { name: 'Revoke Connected Pi' })).toBeEnabled()
    expect(screen.queryByText('Revoked')).not.toBeInTheDocument()
    expect(revokeMCPOAuth).toHaveBeenCalledTimes(1)
  })

  it('unmounts safely while an Allow is pending and does not start a secondary request', async () => {
    oauthFixture()
    const decision = deferred<{ status: string }>()
    vi.mocked(approveMCPOAuth).mockReturnValueOnce(decision.promise)
    const view = render(<MCPSettings />)
    fireEvent.click(await screen.findByRole('button', { name: 'Allow access' }))
    view.unmount()
    await act(async () => decision.resolve({ status: 'approved' }))
    expect(getMCPOAuthConnections).toHaveBeenCalledTimes(1)
    expect(getMCPOAuthPending).toHaveBeenCalledTimes(1)
  })

  it.each(LOCALES)('renders missing connection Last used in $code', async ({ code }) => {
    oauthFixture()
    useI18nStore.getState().setLocale(code, false)
    const view = render(<MCPSettings />)
    await screen.findByText('Connected Pi')
    expect(screen.getByText(`${t('mcp.lastUsed')}: ${t('mcp.noData')}`)).toBeInTheDocument()
    expect(screen.queryByText('Invalid Date')).not.toBeInTheDocument()
    view.unmount()
    useI18nStore.getState().setLocale('en', false)
  })
})


describe('MCPSettings — independent storage warning observations', () => {
  for (const first of ['status', 'connections'] as const) {
    it(`keeps status storage closure when empty connections resolve ${first === 'status' ? 'second' : 'first'}`, async () => {
      let resolveStatus!: (value: MCPStatus) => void
      let resolveConnections!: (value: { connections: [] }) => void
      vi.mocked(getMCPStatus).mockReturnValue(new Promise((resolve) => { resolveStatus = resolve }))
      vi.mocked(getMCPOAuthConnections).mockReturnValue(new Promise((resolve) => { resolveConnections = resolve }))
      render(<MCPSettings />)
      const loaded = { ...status, oauthWarning: storageWarning }
      if (first === 'status') {
        await act(async () => resolveStatus(loaded))
        expect(screen.getByRole('alert')).toHaveTextContent(t('mcp.oauth.storageWarning'))
        await act(async () => resolveConnections({ connections: [] }))
      } else {
        await act(async () => resolveConnections({ connections: [] }))
        await act(async () => resolveStatus(loaded))
      }
      const alert = screen.getByRole('alert')
      expect(alert).toHaveTextContent(t('mcp.oauth.storageWarning'))
      expect(alert).not.toHaveTextContent('This connection is denied')
      expect(alert.parentElement).toHaveAttribute('aria-label', t('mcp.title'))
      expect(screen.queryByText(t('mcp.empty'))).not.toBeInTheDocument()
      expect(screen.queryByText(storageWarning)).not.toBeInTheDocument()
    })
  }

  it('retains successful status and storage warning when the connection request fails', async () => {
    vi.mocked(getMCPStatus).mockResolvedValue({ ...status, oauthWarning: storageWarning })
    vi.mocked(getMCPOAuthConnections).mockRejectedValue(new Error('connections unavailable'))
    render(<MCPSettings />)
    expect(await screen.findByText(t('mcp.oauth.storageWarning'))).toBeInTheDocument()
    expect(screen.getByText(status.endpoint)).toBeInTheDocument()
    expect(screen.queryByText(t('mcp.empty'))).not.toBeInTheDocument()
  })

  it('applies status refresh before pending settles and despite failed connections, preserving unsaved inputs', async () => {
    render(<MCPSettings />)
    await screen.findByText('Not listening')
    fireEvent.change(screen.getByLabelText('Loopback port'), { target: { value: '19444' } })
    fireEvent.click(screen.getByLabelText('Enable MCP'))
    let resolvePending!: (value: { pending: [] }) => void
    vi.mocked(getMCPOAuthPending).mockReturnValue(new Promise((resolve) => { resolvePending = resolve }))
    vi.mocked(getMCPOAuthConnections).mockRejectedValue(new Error('secondary failed'))
    vi.mocked(getMCPStatus).mockResolvedValue({ ...status, port: 19555, endpoint: 'http://127.0.0.1:19555/mcp', oauthWarning: storageWarning })
    fireEvent.click(screen.getByRole('button', { name: 'Refresh metadata' }))
    expect(await screen.findByText(t('mcp.oauth.storageWarning'))).toBeInTheDocument()
    expect(screen.getByText('http://127.0.0.1:19555/mcp')).toBeInTheDocument()
    expect(screen.getByLabelText('Loopback port')).toHaveValue(19444)
    expect(screen.getByLabelText('Enable MCP')).toBeChecked()
    expect(screen.queryByText(t('mcp.empty'))).not.toBeInTheDocument()
    await act(async () => resolvePending({ pending: [] }))
    expect(await screen.findByText(/secondary failed/)).toBeInTheDocument()
    expect(configureMCP).not.toHaveBeenCalled()
    vi.mocked(getMCPStatus).mockResolvedValue(status)
    vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [] })
    vi.mocked(getMCPOAuthPending).mockResolvedValue({ pending: [] })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Refresh metadata' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: 'Refresh metadata' }))
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
    expect(screen.getByText(t('mcp.empty'))).toBeInTheDocument()
  })

  for (const warning of [revocationWarning, 'new storage warning: ' + storageWarning]) {
    it(`classifies only exact wire values: ${warning === revocationWarning ? 'revocation' : 'generic'}`, async () => {
      vi.mocked(getMCPStatus).mockResolvedValue({ ...status, oauthWarning: warning })
      render(<MCPSettings />)
      const key = warning === revocationWarning ? 'mcp.oauth.revokeWarning' : 'mcp.oauth.genericWarning'
      expect(await screen.findByRole('alert')).toHaveTextContent(t(key))
      expect(screen.queryByText(warning)).not.toBeInTheDocument()
    })
  }

  it('uses the connection warning independently of a clean status and clears it after a confirmed read', async () => {
    vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [], warning: storageWarning })
    render(<MCPSettings />)
    expect(await screen.findByText(t('mcp.oauth.storageWarning'))).toBeInTheDocument()
    await screen.findByText('Not listening')
    vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [] })
    fireEvent.click(screen.getByRole('button', { name: 'Refresh metadata' }))
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
  })

  it('does not erase storage closure on an unrelated successful revoke and empty connection read', async () => {
    oauthFixture()
    vi.mocked(getMCPStatus).mockResolvedValue({ ...status, enabled: true, oauthWarning: storageWarning })
    render(<MCPSettings />)
    await screen.findByRole('button', { name: 'Revoke Connected Pi' })
    vi.mocked(revokeMCPOAuth).mockResolvedValue({ status: 'revoked' })
    vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [] })
    fireEvent.click(screen.getByRole('button', { name: 'Revoke Connected Pi' }))
    await waitFor(() => expect(getMCPOAuthConnections).toHaveBeenCalledTimes(2))
    expect(screen.getByText(t('mcp.oauth.storageWarning'))).toBeInTheDocument()
    expect(screen.queryByText(t('mcp.empty'))).not.toBeInTheDocument()
  })
})


describe('MCPSettings — reviewed warning envelope and reconciliation regressions', () => {
  for (const [suffix, key] of [
    [storageWarning, 'mcp.oauth.storageWarning'],
    [revocationWarning, 'mcp.oauth.revokeWarning'],
    ['new failure: ' + storageWarning, 'mcp.oauth.genericWarning'],
    ['', 'mcp.oauth.genericWarning'],
  ] as const) {
    it(`classifies the exact failed-revoke envelope suffix: ${suffix || 'empty'}`, async () => {
      oauthFixture()
      const message = 'MCP_OAUTH_REVOCATION_UNCONFIRMED: ' + suffix
      vi.mocked(revokeMCPOAuth).mockRejectedValue(new ApiError(message, 503, ''))
      render(<MCPSettings />)
      await screen.findByRole('button', { name: 'Revoke Connected Pi' })
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      fireEvent.click(screen.getByRole('button', { name: 'Revoke Connected Pi' }))
      expect(await screen.findByRole('alert')).toHaveTextContent(t(key))
      for (const other of ['mcp.oauth.storageWarning', 'mcp.oauth.revokeWarning', 'mcp.oauth.genericWarning'] as const) {
        if (other !== key) expect(screen.queryByText(t(other))).not.toBeInTheDocument()
      }
      expect(screen.queryByText(message)).not.toBeInTheDocument()
      if (suffix) expect(screen.queryByText(suffix)).not.toBeInTheDocument()
      expect(getMCPStatus).toHaveBeenCalledTimes(1)
    })
  }

  it('retires loaded revocation debt after approval and a newer clean connections read', async () => {
    oauthFixture()
    vi.mocked(getMCPStatus).mockResolvedValue({ ...status, enabled: true, oauthWarning: revocationWarning })
    vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [connection], warning: revocationWarning })
    render(<MCPSettings />)
    expect(await screen.findByRole('alert')).toHaveTextContent(t('mcp.oauth.revokeWarning'))
    await screen.findByRole('button', { name: 'Allow access' })
    vi.mocked(approveMCPOAuth).mockResolvedValue({ status: 'approved' })
    vi.mocked(getMCPOAuthPending).mockResolvedValue({ pending: [] })
    vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [connection] })
    fireEvent.click(screen.getByRole('button', { name: 'Allow access' }))
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
    expect(approveMCPOAuth).toHaveBeenCalledTimes(1)
    expect(revokeMCPOAuth).not.toHaveBeenCalled()
    expect(getMCPStatus).toHaveBeenCalledTimes(1)
    expect(screen.getByText('Connected Pi')).toBeInTheDocument()
  })

  for (const warning of [revocationWarning, storageWarning, 'unknown storage warning']) {
    it(`reconciles only known debt when refreshed status precedes a newer clean connections response: ${warning}`, async () => {
      render(<MCPSettings />)
      await screen.findByText('Not listening')
      fireEvent.change(screen.getByLabelText('Loopback port'), { target: { value: '19444' } })
      fireEvent.click(screen.getByLabelText('Enable MCP'))
      const refreshedStatus = deferred<MCPStatus>()
      const refreshedConnections = deferred<{ connections: [] }>()
      vi.mocked(getMCPStatus).mockReturnValue(refreshedStatus.promise)
      vi.mocked(getMCPOAuthConnections).mockReturnValue(refreshedConnections.promise)
      fireEvent.click(screen.getByRole('button', { name: 'Refresh metadata' }))
      await act(async () => refreshedStatus.resolve({ ...status, oauthWarning: warning }))
      const key = warning === revocationWarning ? 'mcp.oauth.revokeWarning' : warning === storageWarning ? 'mcp.oauth.storageWarning' : 'mcp.oauth.genericWarning'
      expect(screen.getByRole('alert')).toHaveTextContent(t(key))
      await act(async () => refreshedConnections.resolve({ connections: [] }))
      if (warning === revocationWarning) expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      else expect(screen.getByRole('alert')).toHaveTextContent(t(key))
      expect(screen.getByLabelText('Loopback port')).toHaveValue(19444)
      expect(screen.getByLabelText('Enable MCP')).toBeChecked()
    })
  }

  it('retains a newer warning-bearing status if refresh connections resolved first', async () => {
    render(<MCPSettings />)
    await screen.findByText('Not listening')
    const refreshedStatus = deferred<MCPStatus>()
    const refreshedConnections = deferred<{ connections: [] }>()
    vi.mocked(getMCPStatus).mockReturnValue(refreshedStatus.promise)
    vi.mocked(getMCPOAuthConnections).mockReturnValue(refreshedConnections.promise)
    fireEvent.click(screen.getByRole('button', { name: 'Refresh metadata' }))
    await act(async () => refreshedConnections.resolve({ connections: [] }))
    await act(async () => refreshedStatus.resolve({ ...status, oauthWarning: revocationWarning }))
    expect(screen.getByRole('alert')).toHaveTextContent(t('mcp.oauth.revokeWarning'))
  })
})


describe('MCPSettings — storage closure recovery guidance takes presentation precedence', () => {
  it('replaces loaded retryable-debt guidance when a retry reports an uncertain commit', async () => {
    oauthFixture()
    vi.mocked(getMCPStatus).mockResolvedValue({ ...status, enabled: true, oauthWarning: revocationWarning })
    vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [connection], warning: revocationWarning })
    vi.mocked(revokeMCPOAuth).mockRejectedValue(new ApiError('MCP_OAUTH_REVOCATION_UNCONFIRMED: ' + storageWarning, 503, ''))
    render(<MCPSettings />)
    expect(await screen.findByRole('alert')).toHaveTextContent(t('mcp.oauth.revokeWarning'))
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Connected Pi' }))
    expect(await screen.findByText(t('mcp.oauth.storageWarning'))).toBeInTheDocument()
    expect(screen.queryByText(t('mcp.oauth.revokeWarning'))).not.toBeInTheDocument()
    expect(screen.queryByText(/Keep Launcher open and retry/)).not.toBeInTheDocument()
    expect(screen.queryByText(t('mcp.empty'))).not.toBeInTheDocument()
    expect(getMCPStatus).toHaveBeenCalledTimes(1)
  })

  for (const closureSource of ['status', 'connections'] as const) {
    for (const first of ['status', 'connections'] as const) {
      it(`prioritizes closure from ${closureSource} with ${first} resolving first, without erasing stale debt`, async () => {
        const initialStatus = deferred<MCPStatus>()
        const initialConnections = deferred<{ connections: []; warning: string }>()
        vi.mocked(getMCPStatus).mockReturnValue(initialStatus.promise)
        vi.mocked(getMCPOAuthConnections).mockReturnValue(initialConnections.promise)
        render(<MCPSettings />)
        const loaded = { ...status, oauthWarning: closureSource === 'status' ? storageWarning : revocationWarning }
        const saved = { connections: [] as [], warning: closureSource === 'connections' ? storageWarning : revocationWarning }
        if (first === 'status') {
          await act(async () => initialStatus.resolve(loaded))
          await act(async () => initialConnections.resolve(saved))
        } else {
          await act(async () => initialConnections.resolve(saved))
          await act(async () => initialStatus.resolve(loaded))
        }
        expect(screen.getByRole('alert')).toHaveTextContent(t('mcp.oauth.storageWarning'))
        expect(screen.queryByText(t('mcp.oauth.revokeWarning'))).not.toBeInTheDocument()
        // Remove only the closure observation; the other source's debt must
        // reappear, proving precedence did not mutate or discard its state.
        if (closureSource === 'status') {
          vi.mocked(getMCPStatus).mockResolvedValue(status)
          vi.mocked(getMCPOAuthConnections).mockRejectedValue(new Error('connections unavailable'))
        } else {
          vi.mocked(getMCPStatus).mockRejectedValue(new Error('status unavailable'))
          vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [], warning: revocationWarning })
        }
        fireEvent.click(screen.getByRole('button', { name: 'Refresh metadata' }))
        expect(await screen.findByText(t('mcp.oauth.revokeWarning'))).toBeInTheDocument()
        expect(screen.queryByText(t('mcp.oauth.storageWarning'))).not.toBeInTheDocument()
      })
    }
  }

  it('keeps an unknown source warning generic alongside known storage closure', async () => {
    vi.mocked(getMCPStatus).mockResolvedValue({ ...status, oauthWarning: storageWarning })
    vi.mocked(getMCPOAuthConnections).mockResolvedValue({ connections: [], warning: 'unknown warning with ' + revocationWarning })
    render(<MCPSettings />)
    expect(await screen.findByText(t('mcp.oauth.storageWarning'))).toBeInTheDocument()
    expect(await screen.findByText(t('mcp.oauth.genericWarning'))).toBeInTheDocument()
    expect(screen.queryByText(t('mcp.oauth.revokeWarning'))).not.toBeInTheDocument()
  })
})
