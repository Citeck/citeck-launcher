import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { describe, it, expect, beforeAll, beforeEach, vi } from 'vitest'
import { DependenciesDialog } from './DependenciesDialog'
import { useDepsStore } from '../lib/depsStore'
import { useUpdateStore } from '../lib/updateStore'
import { getDependencies, getDependencyPreflight, postDependencyMigrate } from '../lib/api'
import { showError } from '../lib/errorModal'
import type { DependenciesDto } from '../lib/types'

vi.mock('../lib/api', () => ({
  getDependencies: vi.fn(),
  getDependencyPreflight: vi.fn(),
  postDependencyMigrate: vi.fn(),
  getUpdateStatus: vi.fn(),
  checkUpdate: vi.fn(),
}))
vi.mock('../lib/errorModal', () => ({ showError: vi.fn() }))
vi.mock('../lib/desktop', () => ({ isDesktopModeSync: vi.fn(() => false) }))

const items = [
  {
    id: 'postgres', app: 'postgres', currentImage: 'postgres:17.5', currentVersion: '17.5',
    targetImage: 'postgres:18', targetVersion: '18', status: 'upgrade-available', migratable: true,
  },
  {
    id: 'rabbitmq', app: 'rabbitmq', currentImage: 'rabbitmq:4.1.2-management', currentVersion: '4.1.2',
    targetImage: 'rabbitmq:4.2.9-management', targetVersion: '4.2.9', status: 'requires-launcher-update', migratable: false,
  },
]

const okPreflight = {
  ok: true, problems: [], warnings: [], from: 'postgres:17.5', to: 'postgres:18',
  dataSizeBytes: 10, requiredHostBytes: 20, requiredVolumeBytes: 20,
  freeHostBytes: 100, freeVolumeBytes: 100, wasRunning: true,
}

function mockDeps(dto: Partial<DependenciesDto> = {}) {
  vi.mocked(getDependencies).mockResolvedValue({ items, ...dto })
}

beforeAll(() => {
  // jsdom doesn't implement <dialog> showModal/close — stub them.
  // The `open` flag matters: contents of a closed <dialog> are hidden, and
  // role queries skip hidden elements.
  HTMLDialogElement.prototype.showModal = vi.fn(function (this: HTMLDialogElement) { this.open = true })
  HTMLDialogElement.prototype.close = vi.fn(function (this: HTMLDialogElement) { this.open = false })
})

beforeEach(() => {
  vi.mocked(getDependencies).mockReset()
  vi.mocked(getDependencyPreflight).mockReset()
  vi.mocked(postDependencyMigrate).mockReset()
  vi.mocked(showError).mockReset()
  mockDeps()
  vi.mocked(getDependencyPreflight).mockResolvedValue(okPreflight)
  vi.mocked(postDependencyMigrate).mockResolvedValue({ success: true, message: 'started' })
  useDepsStore.setState({ migration: null, result: null, dismissedKey: null, data: null })
  useUpdateStore.setState({ status: null })
})

describe('DependenciesDialog', () => {
  it('lists dependencies and offers Upgrade only for migratable ones', async () => {
    render(<DependenciesDialog open onClose={() => {}} />)
    await screen.findByTestId('dep-postgres')
    expect(screen.getByTestId('dep-postgres')).toHaveTextContent('postgres:18')
    expect(screen.getAllByRole('button', { name: /^upgrade$/i })).toHaveLength(1)
    // The one this launcher cannot migrate says so instead of offering a button.
    expect(screen.getByTestId('dep-rabbitmq').textContent).toMatch(/citeck update/i)
  })

  it('blocks Start on a failed preflight and on an unconfirmed existing volume', async () => {
    vi.mocked(getDependencyPreflight).mockResolvedValue({ ...okPreflight, ok: false, problems: ['no space'] })
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /^upgrade$/i }))
    await screen.findByRole('alert')
    expect(screen.getByRole('button', { name: /start migration/i })).toBeDisabled()

    vi.mocked(getDependencyPreflight).mockResolvedValue({
      ...okPreflight,
      existingTargetVolume: { name: 'postgres3', sizeBytes: 5, version: 'empty' },
    })
    fireEvent.click(screen.getByRole('button', { name: /back/i }))
    fireEvent.click(await screen.findByRole('button', { name: /^upgrade$/i }))
    const checkbox = await screen.findByRole('checkbox')
    expect(screen.getByRole('button', { name: /start migration/i })).toBeDisabled()
    fireEvent.click(checkbox)
    expect(screen.getByRole('button', { name: /start migration/i })).toBeEnabled()
    fireEvent.click(screen.getByRole('button', { name: /start migration/i }))
    await waitFor(() => expect(postDependencyMigrate).toHaveBeenCalledWith('postgres', true))
  })

  it('reports a refused migration on the shared error modal', async () => {
    vi.mocked(postDependencyMigrate).mockRejectedValue(new Error('volume postgres3 already exists'))
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /^upgrade$/i }))
    fireEvent.click(await screen.findByRole('button', { name: /start migration/i }))
    await waitFor(() => expect(showError).toHaveBeenCalled())
  })

  // The daemon launches the migration goroutine BEFORE writing the 202, so a
  // fast failure can be over by the time the POST answers. Restarting an empty
  // migration then would null the verdict and strand the dialog on a progress
  // screen for something that already finished.
  it('does not resurrect an empty migration over a verdict that already arrived', async () => {
    vi.mocked(postDependencyMigrate).mockImplementation(async () => {
      useDepsStore.getState().onStart('postgres', 10)
      useDepsStore.getState().onError('postgres', 'restore failed')
      return { success: true, message: 'started' }
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /^upgrade$/i }))
    fireEvent.click(await screen.findByRole('button', { name: /start migration/i }))
    expect(await screen.findByTestId('deps-result')).toHaveTextContent('restore failed')
    expect(screen.queryByTestId('deps-progress')).toBeNull()
  })

  // The optimistic start must never REPLACE a migration it did not start: it
  // carries no step, no count and no history, so adopting it over live state
  // would blank the progress list.
  it('never replaces a migration already in the store', async () => {
    vi.mocked(postDependencyMigrate).mockImplementation(async () => {
      useDepsStore.getState().onStart('rabbitmq', 10)
      useDepsStore.getState().onProgress({ appName: 'rabbitmq', phase: 'dump', current: 4, total: 10, percent: 42, after: '' })
      return { success: true, message: 'started' }
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /^upgrade$/i }))
    fireEvent.click(await screen.findByRole('button', { name: /start migration/i }))
    await screen.findByTestId('deps-progress')
    const m = useDepsStore.getState().migration!
    expect(m.id).toBe('rabbitmq')
    expect(m.step).toBe('dump')
    expect(m.stepCount).toBe(10)
  })

  it('shows progress from the store and the result at the end', async () => {
    render(<DependenciesDialog open onClose={() => {}} />)
    await screen.findByTestId('dep-postgres')
    act(() => {
      useDepsStore.getState().onStart('postgres', 10)
      useDepsStore.getState().onProgress({ appName: 'postgres', phase: 'dump', current: 4, total: 10, percent: 42, after: 'dumped 1 GiB' })
    })
    const progress = await screen.findByTestId('deps-progress')
    expect(progress).toHaveTextContent('42%')
    expect(progress).toHaveTextContent('dumped 1 GiB')
    // Every known step is listed; the ones already past are checked off.
    expect(progress).toHaveTextContent(/verifying/i)
    expect(screen.getByTestId('deps-step-stop-namespace')).toHaveAttribute('data-state', 'done')
    expect(screen.getByTestId('deps-step-dump')).toHaveAttribute('data-state', 'active')
    expect(screen.getByTestId('deps-step-restore')).toHaveAttribute('data-state', 'todo')

    act(() => useDepsStore.getState().onError('postgres', 'restore failed'))
    expect(await screen.findByTestId('deps-result')).toHaveTextContent('restore failed')
  })

  // Closing cancels nothing (the migration runs on the daemon), so the button
  // that closes must never be the one thing the user cannot reach.
  it('never disables Close, and reopening shows the step the migration is on', async () => {
    const onClose = vi.fn()
    const { rerender } = render(<DependenciesDialog open onClose={onClose} />)
    await screen.findByTestId('dep-postgres')
    act(() => {
      useDepsStore.getState().onStart('postgres', 10)
      useDepsStore.getState().onProgress({ appName: 'postgres', phase: 'dump', current: 4, total: 10, percent: 42, after: '' })
    })
    // Both the header X and the footer button close it; neither may be disabled.
    const closeButtons = screen.getAllByRole('button', { name: /^close$/i })
    closeButtons.forEach((b) => expect(b).toBeEnabled())
    fireEvent.click(closeButtons[closeButtons.length - 1])
    expect(onClose).toHaveBeenCalled()

    rerender(<DependenciesDialog open={false} onClose={onClose} />)
    rerender(<DependenciesDialog open onClose={onClose} />)
    expect(screen.getByTestId('deps-step-dump')).toHaveAttribute('data-state', 'active')
  })

  // A pending rollback freezes the pin and the daemon refuses every new
  // migration until it succeeds — offering Upgrade would be a button whose
  // only possible answer is a 409.
  it('shows a pending rollback and refuses to start anything while it is set', async () => {
    mockDeps({ rollbackPending: 'a previous migration of postgres left a rollback pending' })
    render(<DependenciesDialog open onClose={() => {}} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('rollback pending')
    expect(screen.getByRole('button', { name: /^upgrade$/i })).toBeDisabled()
  })

  it('reports the verdict of the last migration on the list', async () => {
    mockDeps({
      lastResult: {
        id: 'postgres', from: 'postgres:17.5', to: 'postgres:18',
        finishedAt: 1, success: false, error: 'restore failed',
      },
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    const last = await screen.findByTestId('deps-last-result')
    expect(last).toHaveTextContent('restore failed')
    expect(last).toHaveTextContent('postgres:18')
  })

  it('surfaces a failed list fetch on the shared error modal', async () => {
    vi.mocked(getDependencies).mockRejectedValue(new Error('daemon is gone'))
    render(<DependenciesDialog open onClose={() => {}} />)
    await waitFor(() => expect(showError).toHaveBeenCalled())
  })
})
