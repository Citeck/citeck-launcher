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

// The desktop shape: the dump lands on the host and the cluster inside the
// Docker VM, so the two halves are checked against two different filesystems.
const okPreflight = {
  ok: true, problems: [], warnings: [], from: 'postgres:17.5', to: 'postgres:18',
  dataSizeBytes: 10, requiredHostBytes: 20, requiredVolumeBytes: 20,
  freeHostBytes: 100, freeVolumeBytes: 100, wasRunning: true,
  sharedFilesystem: false, requiredTotalBytes: 0,
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
  useDepsStore.setState({ migration: null, result: null, dismissedKey: null, data: null, rollbackPending: '' })
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

  // The list and the alarm are two different reads of the daemon. Since the
  // daemon began carrying the alarm in NamespaceDto, the ordinary namespace
  // fetch learns it first — a dialog still holding the payload from before it
  // happened would otherwise offer an Upgrade button whose only possible
  // answer is a 409.
  it('refuses to start on a rollback the namespace fetch learned after the list was read', async () => {
    render(<DependenciesDialog open onClose={() => {}} />)
    await screen.findByTestId('dep-postgres')
    expect(screen.getByRole('button', { name: /^upgrade$/i })).toBeEnabled()

    // What `fetchData` does with NamespaceDto.dependencyRollbackPending.
    act(() => useDepsStore.getState().setRollbackPending('a previous migration of postgres left a rollback pending'))
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

  // The confirm screen maps over both lists. The daemon leaves them nil on the
  // ORDINARY happy path, and a nil Go slice marshals as `null` — so an
  // unguarded map is the confirm screen crashing in the common case.
  it('renders a preflight whose problem and warning lists are null', async () => {
    vi.mocked(getDependencyPreflight).mockResolvedValue({
      ...okPreflight,
      problems: null as unknown as string[],
      warnings: null as unknown as string[],
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /^upgrade$/i }))
    expect(await screen.findByRole('button', { name: /start migration/i })).toBeEnabled()
  })

  // A preflight the daemon refused before it touched Docker measured nothing;
  // its zeros would claim the namespace holds no data and the host has no free
  // space, above the line that gives the actual reason.
  it('does not render the size block of a preflight that measured nothing', async () => {
    vi.mocked(getDependencyPreflight).mockResolvedValue({
      ok: false, problems: ['a previous migration of postgres left a rollback pending'], warnings: [],
      from: 'postgres:17.5', to: 'postgres:18',
      dataSizeBytes: 0, requiredHostBytes: 0, requiredVolumeBytes: 0,
      freeHostBytes: 0, freeVolumeBytes: 0, wasRunning: false,
      sharedFilesystem: false, requiredTotalBytes: 0,
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /^upgrade$/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent('rollback pending')
    expect(screen.queryByText(/Data size:/)).toBeNull()
    expect(screen.queryByText(/Host \(dump\):/)).toBeNull()
    // The rest of the confirm screen is untouched.
    expect(screen.getByText(/current data volume is kept untouched/i)).toBeInTheDocument()
  })

  it('renders the size block of a preflight that did measure', async () => {
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /^upgrade$/i }))
    expect(await screen.findByText(/Host \(dump\):/)).toBeInTheDocument()
  })

  // "empty" is the daemon's sentinel for a volume with no cluster in it — the
  // common leftover case — and it is not a version: interpolating it reads as
  // "PostgreSQL empty" in every locale.
  it('describes an empty leftover volume without calling it a PostgreSQL version', async () => {
    vi.mocked(getDependencyPreflight).mockResolvedValue({
      ...okPreflight,
      existingTargetVolume: { name: 'postgres3', sizeBytes: 1024, version: 'empty' },
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /^upgrade$/i }))
    const label = await screen.findByText(/postgres3/)
    expect(label).toHaveTextContent(/no cluster in it/i)
    expect(label).not.toHaveTextContent(/PostgreSQL empty/i)
  })

  // A step id this launcher has no key for — a newer daemon's plan — must read
  // as the id, never as the bare lookup key.
  it('renders an unknown step id as the id, not as its locale key', async () => {
    render(<DependenciesDialog open onClose={() => {}} />)
    await screen.findByTestId('dep-postgres')
    act(() => {
      useDepsStore.getState().onStart('postgres', 11)
      useDepsStore.getState().onProgress({ appName: 'postgres', phase: 'reindex', current: 11, total: 11, percent: 0, after: '' })
    })
    const step = await screen.findByTestId('deps-step-reindex')
    expect(step).toHaveTextContent('reindex')
    expect(step).not.toHaveTextContent('deps.step.reindex')
  })

  // The daemon publishes "preparing" with no step count while it builds the
  // plan (a `du` of the data volume, minutes on a real cluster). The step list
  // is meaningless then — nothing in it has been decided, let alone started.
  it('shows a spinner line, not a step list, while the daemon is preparing', async () => {
    render(<DependenciesDialog open onClose={() => {}} />)
    await screen.findByTestId('dep-postgres')
    act(() => useDepsStore.getState().onStart('postgres', 0))
    expect(await screen.findByTestId('deps-preparing')).toHaveTextContent(/preparing/i)
    expect(screen.queryByTestId('deps-step-dump')).toBeNull()

    // Once the plan exists the list takes over.
    act(() => useDepsStore.getState().onProgress({
      appName: 'postgres', phase: 'dump', current: 4, total: 10, percent: 0, after: '',
    }))
    expect(await screen.findByTestId('deps-step-dump')).toHaveAttribute('data-state', 'active')
    expect(screen.queryByTestId('deps-preparing')).toBeNull()
  })

  // A migration can vanish WITHOUT a verdict — the daemon died mid-migration
  // and its restart's recovery rolled it back silently. The dialog used to sit
  // on a progress screen with nothing in it: a blank modal body.
  it('falls back to the list when a migration disappears with no verdict', async () => {
    render(<DependenciesDialog open onClose={() => {}} />)
    await screen.findByTestId('dep-postgres')
    act(() => useDepsStore.getState().onStart('postgres', 0))
    await screen.findByTestId('deps-progress')

    act(() => useDepsStore.getState().hydrate(null))
    await waitFor(() => expect(screen.queryByTestId('deps-progress')).toBeNull())
    expect(await screen.findByTestId('dep-postgres')).toBeInTheDocument()
    expect(screen.queryByTestId('deps-result')).toBeNull()
  })

  it('surfaces a failed list fetch on the shared error modal', async () => {
    vi.mocked(getDependencies).mockRejectedValue(new Error('daemon is gone'))
    render(<DependenciesDialog open onClose={() => {}} />)
    await waitFor(() => expect(showError).toHaveBeenCalled())
  })

  // The banner lists a held-back upgrade by its dependency ID and so do the
  // progress panel, the verdict and the CLI (`citeck deps upgrade <id>`); the
  // dialog named the same thing by its generated APP name. Those differ for
  // exactly one dependency — id `mongodb`, container `mongo` — so on the one
  // stand where it matters the two surfaces the user has open at the same time
  // disagreed. The id wins: it is the stable API value, the pin-map key and
  // what every other surface already shows.
  it('names a dependency by its id, the way the banner and the CLI do', async () => {
    mockDeps({
      items: [{
        id: 'mongodb', app: 'mongo', currentImage: 'mongo:6', currentVersion: '6',
        targetImage: 'mongo:7', targetVersion: '7', status: 'upgrade-available', migratable: true,
      }],
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    const row = await screen.findByTestId('dep-mongodb')
    expect(row.querySelector('td')).toHaveTextContent(/^mongodb$/)

    fireEvent.click(screen.getByRole('button', { name: /^upgrade$/i }))
    expect(await screen.findByText(/Migrate mongodb from/)).toBeInTheDocument()
  })

  // The checkbox is the only thing that sets it, so the ordinary migration —
  // no leftover volume, nobody confirming anything — posts false. Sending true
  // by accident would authorise deleting a volume the user was never asked
  // about.
  it('posts replaceExistingVolume=false when nothing was confirmed', async () => {
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /^upgrade$/i }))
    fireEvent.click(await screen.findByRole('button', { name: /start migration/i }))
    await waitFor(() => expect(postDependencyMigrate).toHaveBeenCalledWith('postgres', false))
  })

  // A newer daemon's plan can name steps this launcher has never heard of. They
  // are APPENDED to the known plan rather than dropped or spliced in — dropping
  // one hides work that is running, and the known list is the order this
  // launcher can vouch for. Both an unknown step already DONE and the unknown
  // step running have to appear.
  it('appends unknown step ids after the known plan, done ones included', async () => {
    render(<DependenciesDialog open onClose={() => {}} />)
    await screen.findByTestId('dep-postgres')
    act(() => {
      useDepsStore.getState().onStart('postgres', 12)
      useDepsStore.getState().onProgress({ appName: 'postgres', phase: 'reindex', current: 11, total: 12, percent: 0, after: '' })
      useDepsStore.getState().onProgress({ appName: 'postgres', phase: 'revacuum', current: 12, total: 12, percent: 0, after: '' })
    })
    expect(await screen.findByTestId('deps-step-reindex')).toHaveAttribute('data-state', 'done')
    expect(screen.getByTestId('deps-step-revacuum')).toHaveAttribute('data-state', 'active')
    // Order: the whole known plan first, then the unknown ones as they appeared.
    const ids = screen.getAllByTestId(/^deps-step-/).map((li) => li.getAttribute('data-testid'))
    expect(ids.slice(-3)).toEqual(['deps-step-stop-target', 'deps-step-reindex', 'deps-step-revacuum'])
    // Nothing was dropped: the known plan is still all there.
    expect(ids).toHaveLength(12)
  })

  // The daemon can die mid-migration; its restart rolls back, and if THAT
  // fails the journal stays open — the pin is frozen and every new migration is
  // refused. That recovery emits no event and produces no result, so the
  // dialog's migration simply vanishes (hydrate(null)). Falling back to the
  // list is not enough: the list it had was fetched before any of this, so it
  // would show a healthy set of dependencies with an Upgrade button that can
  // only ever answer 409. The end of a migration is a reason to re-read.
  it('shows the pending rollback when a migration vanishes with no verdict', async () => {
    render(<DependenciesDialog open onClose={() => {}} />)
    await screen.findByTestId('dep-postgres')
    act(() => useDepsStore.getState().onStart('postgres', 10))
    await screen.findByTestId('deps-progress')

    // What the daemon's failed recovery leaves behind, learned only by asking.
    mockDeps({ rollbackPending: 'a previous migration of postgres left a rollback pending' })
    act(() => useDepsStore.getState().hydrate(null))
    expect(await screen.findByRole('alert')).toHaveTextContent('rollback pending')
    expect(screen.queryByTestId('deps-progress')).toBeNull()
    expect(screen.getByRole('button', { name: /^upgrade$/i })).toBeDisabled()
  })

  // On the ordinary SERVER layout the dump and the new cluster are written to
  // ONE filesystem and exist side by side until the commit, so what has to fit
  // is the two halves ADDED UP. Rendering them as two independent lines told
  // the user that 20 B had to fit and that 100 B was free — twice — while the
  // daemon was refusing the migration for wanting 40 B. One line, the real
  // number.
  it('states one combined requirement when both halves land on one filesystem', async () => {
    vi.mocked(getDependencyPreflight).mockResolvedValue({
      ...okPreflight,
      sharedFilesystem: true, requiredTotalBytes: 40,
      // Two measurements of one filesystem, by two different mechanisms (a host
      // statfs and a df inside a container). The daemon believes the smaller
      // one — it is the one that can run out — so the dialog must not quote the
      // bigger and look roomier than the refusal it is about to get.
      freeHostBytes: 100, freeVolumeBytes: 90,
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /^upgrade$/i }))
    const line = await screen.findByText(/dump \+ new cluster/i)
    expect(line).toHaveTextContent('40 B')
    expect(line).toHaveTextContent('90 B')
    // Never beside the two halves it replaces — that is the understatement.
    expect(screen.queryByText(/Host \(dump\):/)).toBeNull()
    expect(screen.queryByText(/^Volumes:/)).toBeNull()
    // The data size is unaffected: it is one measurement of one volume.
    expect(screen.getByText(/Data size:/)).toBeInTheDocument()
  })

  // An unmeasurable half is left at 0 by the daemon (with a problem line of its
  // own saying so), not at a real "nothing free" — quoting it as the smaller
  // free would turn a measurement failure into a fake out-of-space claim.
  it('ignores an unmeasured half when it quotes the free space', async () => {
    vi.mocked(getDependencyPreflight).mockResolvedValue({
      ...okPreflight,
      sharedFilesystem: true, requiredTotalBytes: 40,
      freeHostBytes: 0, freeVolumeBytes: 90,
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /^upgrade$/i }))
    expect(await screen.findByText(/dump \+ new cluster/i)).toHaveTextContent('90 B')
  })
})
