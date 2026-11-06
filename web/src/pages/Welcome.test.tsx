import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router'
import { Welcome } from './Welcome'
import { TabBar } from '../components/TabBar'
import { useDaemonStatusStore } from '../lib/daemonStatus'
import { useQuickStartStore } from '../lib/quickstart'
import type { DaemonStatusDto, QuickStartDto } from '../lib/types'

const api = vi.hoisted(() => ({
  ApiError: class ApiError extends Error {},
  getDaemonStatus: vi.fn(), getNamespaces: vi.fn(), getQuickStarts: vi.fn(),
  listWorkspaces: vi.fn(), postWorkspaceUpdate: vi.fn(), createNamespace: vi.fn(),
  closeAllDesktopWindows: vi.fn().mockResolvedValue(undefined),
}))
vi.mock('../lib/api', () => api)
vi.mock('../lib/store', () => {
  const state = { namespace: null, fetchData: vi.fn(), startEventStream: vi.fn() }
  return { useDashboardStore: (select: (s: typeof state) => unknown) => select(state) }
})
vi.mock('../components/UpdateNotification', () => ({ UpdateNotification: () => null }))
vi.mock('../components/NamespaceDialog', () => ({ NamespaceDialog: () => null }))
vi.mock('../components/NamespaceEditDialog', () => ({ NamespaceEditDialog: () => null }))
vi.mock('../components/StartProgressStepper', () => ({ StartProgressStepper: () => null }))
vi.mock('../components/RegistryPreflight', () => ({ useRegistryPreflight: () => ({ preflight: vi.fn(), dialog: null }) }))

const status = { desktop: true, workspace: 'default' } as DaemonStatusDto
const oldQuick: QuickStartDto = { name: 'Quick Start With Demo Data', template: 'default', snapshot: 'demo-2026.2', bundleRef: 'community:2026.2' }
const newQuick: QuickStartDto = { ...oldQuick, snapshot: 'demo-2026.3', bundleRef: 'community:2026.3' }

beforeEach(() => {
  vi.clearAllMocks()
  useDaemonStatusStore.setState({ status: { ...status }, loading: false, error: null })
  useQuickStartStore.getState().dismiss()
  api.getDaemonStatus.mockImplementation(async () => ({ ...status }))
  api.getNamespaces.mockResolvedValue([])
  api.getQuickStarts.mockResolvedValue([oldQuick])
  api.listWorkspaces.mockResolvedValue([{ id: 'default', name: 'Citeck', active: true, namespaces: 0 }])
  api.postWorkspaceUpdate.mockResolvedValue({ success: true })
  // Stop before any namespace start: we only inspect the create payload.
  api.createNamespace.mockRejectedValue(new Error('fixture: stop after create request'))
})

function renderWelcome() {
  return render(<MemoryRouter initialEntries={['/welcome']}><TabBar /><Welcome /></MemoryRouter>)
}

it('refreshes Quick Start labels and snapshot payload after Force Update of the same workspace', async () => {
  renderWelcome()
  expect(await screen.findByText('community:2026.2')).toBeInTheDocument()
  api.getQuickStarts.mockResolvedValue([newQuick])
  fireEvent.click(await screen.findByRole('button', { name: 'Force Update' }))
  expect(await screen.findByText('community:2026.3')).toBeInTheDocument()
  expect(screen.queryByText('community:2026.2')).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: /Quick Start With Demo Data/ }))
  await waitFor(() => expect(api.createNamespace).toHaveBeenCalledWith(expect.objectContaining({
    snapshot: 'demo-2026.3', workspaceId: 'default', template: 'default',
  })))
})

it('does not reload Quick Start after a failed Force Update', async () => {
  renderWelcome()
  expect(await screen.findByText('community:2026.2')).toBeInTheDocument()
  const calls = api.getQuickStarts.mock.calls.length
  api.postWorkspaceUpdate.mockRejectedValueOnce(new Error('fixture: update failed'))
  fireEvent.click(await screen.findByRole('button', { name: 'Force Update' }))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Force Update' })).toBeEnabled())
  expect(api.getQuickStarts).toHaveBeenCalledTimes(calls)
  expect(screen.getByText('community:2026.2')).toBeInTheDocument()
})

it.each(['success', 'failure'])('ignores an older in-flight %s after workspace metadata was refreshed', async (outcome) => {
  let finishOld!: (qs: QuickStartDto[]) => void
  let failOld!: (error: Error) => void
  api.getQuickStarts.mockImplementationOnce(() => new Promise<QuickStartDto[]>((resolve, reject) => { finishOld = resolve; failOld = reject }))
  renderWelcome()
  await waitFor(() => expect(api.getQuickStarts).toHaveBeenCalledTimes(1))
  api.getQuickStarts.mockResolvedValue([newQuick])
  fireEvent.click(await screen.findByRole('button', { name: 'Force Update' }))
  expect(await screen.findByText('community:2026.3')).toBeInTheDocument()
  await act(async () => {
    if (outcome === 'success') finishOld([oldQuick])
    else failOld(new Error('fixture: stale load failure'))
  })
  expect(screen.getByText('community:2026.3')).toBeInTheDocument()
  expect(screen.queryByText('community:2026.2')).not.toBeInTheDocument()
})

it('reloads Quick Start after switching workspaces', async () => {
  renderWelcome()
  expect(await screen.findByText('community:2026.2')).toBeInTheDocument()
  api.getQuickStarts.mockResolvedValue([newQuick])
  await act(async () => { useDaemonStatusStore.setState({ status: { ...status, workspace: 'other' } }) })
  expect(await screen.findByText('community:2026.3')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: /Quick Start With Demo Data/ }))
  await waitFor(() => expect(api.createNamespace).toHaveBeenCalledWith(expect.objectContaining({ workspaceId: 'other', snapshot: 'demo-2026.3' })))
})

it('cancels a pending transient retry when Welcome unmounts', async () => {
  vi.useFakeTimers()
  try {
    api.getQuickStarts.mockRejectedValueOnce(new Error('503 Service Unavailable'))
    const { unmount } = renderWelcome()
    await act(async () => { await Promise.resolve() })
    expect(api.getQuickStarts).toHaveBeenCalledTimes(1)
    unmount()
    await act(async () => { await vi.advanceTimersByTimeAsync(1500) })
    expect(api.getQuickStarts).toHaveBeenCalledTimes(1)
  } finally {
    vi.useRealTimers()
  }
})
