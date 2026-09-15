import { render, screen, waitFor, fireEvent, act, within } from '@testing-library/react'
import { describe, it, expect, beforeAll, beforeEach, vi } from 'vitest'
import { DependenciesDialog } from './DependenciesDialog'
import { useDepsStore } from '../lib/depsStore'
import { useUpdateStore } from '../lib/updateStore'
import {
  getDependencies, getDependencyPreflight, postDependencyMigrate,
  getDependencyRollbackPreflight, postDependencyRollback,
} from '../lib/api'
import { showError } from '../lib/errorModal'
import type { DependenciesDto } from '../lib/types'

vi.mock('../lib/api', () => ({
  getDependencies: vi.fn(),
  getDependencyPreflight: vi.fn(),
  postDependencyMigrate: vi.fn(),
  getDependencyRollbackPreflight: vi.fn(),
  postDependencyRollback: vi.fn(),
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
  sharedFilesystem: false, requiredTotalBytes: 0, spaceChecked: true,
}

// What migrate.RollbackPreflight answers: it measures NOTHING (nothing is
// created by a rollback), and its warnings END with the three consequence
// sentences the dialog restates in the user's own language.
const rollbackConsequences = [
  'the namespace will run postgres:17.5 again, on the data in volume postgres2 as it was when the migration to postgres:18.6 finished',
  'everything written since then is in volume postgres3: the launcher keeps it and will not read it again, so that data becomes unreachable',
  'there is no roll-forward: to go back to postgres:18.6 you would migrate again, from the postgres:17.5 data, into a new volume',
]

const okRollbackPreflight = {
  ok: true, problems: [], warnings: [...rollbackConsequences],
  from: 'postgres:18.6', to: 'postgres:17.5',
  dataSizeBytes: 0, requiredHostBytes: 0, requiredVolumeBytes: 0,
  freeHostBytes: 0, freeVolumeBytes: 0, wasRunning: true,
  sharedFilesystem: false, requiredTotalBytes: 0, spaceChecked: false,
}

/** A namespace that HAS migrated postgres, so it carries a rollback offer. */
const migratedPostgres = {
  id: 'postgres', app: 'postgres', currentImage: 'postgres:18.6', currentVersion: '18.6',
  targetImage: 'postgres:18.6', targetVersion: '18.6', status: 'up-to-date', migratable: true,
  rollback: {
    toImage: 'postgres:17.5', toVersion: '17.5', volume: 'postgres2', frozenVolume: 'postgres3',
    migratedAt: Date.UTC(2026, 8, 9, 9, 41), available: true,
  },
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
  vi.mocked(getDependencyRollbackPreflight).mockReset()
  vi.mocked(postDependencyRollback).mockReset()
  vi.mocked(showError).mockReset()
  mockDeps()
  vi.mocked(getDependencyPreflight).mockResolvedValue(okPreflight)
  vi.mocked(postDependencyMigrate).mockResolvedValue({ success: true, message: 'started' })
  vi.mocked(getDependencyRollbackPreflight).mockResolvedValue(okRollbackPreflight)
  vi.mocked(postDependencyRollback).mockResolvedValue({ success: true, message: 'started' })
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

  // The Dependency and Current columns had no horizontal separation of any
  // kind: the header read "DependencyCurrent" and a long id ran straight into
  // its own version — "zookeeperzookeeper:3.9.5", "postgrespostgres:18.6".
  // Tailwind's preflight collapses table borders, which also drops the
  // browser's border-spacing, so the ONLY thing that can hold two cells apart
  // is padding inside the cell. A gap that exists merely because the text
  // happens to be short is not separation, which is why this is asserted on
  // every cell that has another one to its right rather than on a measurement
  // jsdom cannot make (it applies no Tailwind stylesheet).
  it('separates every table column from the one to its right', async () => {
    mockDeps({
      items: [{
        ...items[0], id: 'zookeeper', app: 'zookeeper',
        currentImage: 'zookeeper:3.9.5', currentVersion: '3.9.5',
        targetImage: 'zookeeper:3.9.5', targetVersion: '3.9.5', status: 'up-to-date',
      }],
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    const row = await screen.findByTestId('dep-zookeeper')
    const heads = Array.from(row.closest('table')!.querySelectorAll('thead th'))
    const cells = Array.from(row.querySelectorAll('td'))
    expect(heads).toHaveLength(5)
    expect(cells).toHaveLength(5)
    // The last column is the right-aligned actions cell: nothing follows it.
    for (const el of [...heads.slice(0, -1), ...cells.slice(0, -1)]) {
      const padded = el.className.split(/\s+/).filter((c) => /^(?:pr|px)-/.test(c))
      expect(padded, `<${el.tagName.toLowerCase()} class="${el.className}"> has no trailing padding`)
        .not.toHaveLength(0)
    }
  })

  // The gutter above is padding INSIDE the cell, so it only separates two
  // columns while the text stays inside its own content box — and in an auto
  // table nothing guarantees that. Measured on the running stand (Chromium 141
  // and the desktop app's real WebKitGTK 2.52.3 agree to within 1px): a
  // namespace whose keycloak image is registry-qualified
  // (`keycloak/keycloak:26.4.5`) needs 189px in EACH image column, so the
  // table's own min-content is 647px inside a 606px dialog body — the table is
  // pushed out of the modal and the actions column ends up behind a horizontal
  // scrollbar. Squeeze it further and the unbreakable strings paint straight
  // over the padding into the next column: forcing the first column to 40px
  // put "Зависимость" 48.4px (Chromium) / 48.3px (WebKitGTK) outside its box.
  //
  // Two different wrap rules fix that, and the difference between them is the
  // whole design, so the test names which column gets which:
  //   * the DATA of the image columns wraps `anywhere`, which is the only one
  //     of the two that lowers the cell's min-content — that is what lets the
  //     table shrink to whatever the dialog gives it instead of overflowing;
  //   * everything else (the four headers, the id, the status prose) wraps
  //     `break-word`, which does NOT change intrinsic sizing, so the everyday
  //     layout is untouched and the wrap happens only when a column ends up
  //     narrower than the word it holds. It is on the HEADERS that this pair
  //     sets the columns' floor: a column is never narrower than its own
  //     label, and even that floor cannot produce a collision.
  // jsdom applies no stylesheet, so a width assertion is impossible here; the
  // real evidence is the two engines' measurements. This pins the structure.
  it('lets a long image wrap instead of pushing the table out of the dialog', async () => {
    mockDeps({
      items: [{
        ...items[0], id: 'keycloak', app: 'keycloak',
        currentImage: 'keycloak/keycloak:26.4.5', currentVersion: '26.4.5',
        targetImage: 'keycloak/keycloak:27.0.1', targetVersion: '27.0.1',
      }],
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    const row = await screen.findByTestId('dep-keycloak')
    const heads = Array.from(row.closest('table')!.querySelectorAll('thead th'))
    const cells = Array.from(row.querySelectorAll('td'))
    const has = (el: Element, cls: string) => el.className.split(/\s+/).includes(cls)

    // The two image columns — and only those — may break mid-token, and both
    // carry the floor that keeps the ordinary images off it: lowering a
    // column's min-content also lowers its share of the surplus, which in the
    // desktop webview left it 3px short of `zookeeper:3.9.5` and wrapped that
    // string's last character onto a line of its own.
    for (const i of [1, 2]) {
      expect(has(cells[i], 'wrap-anywhere'), `image cell ${i}: "${cells[i].className}"`).toBe(true)
      expect(has(cells[i], 'min-w-[calc(15ch_+_1rem)]'), `image cell ${i}: "${cells[i].className}"`).toBe(true)
    }
    // Everything else keeps its intrinsic width and merely refuses to overflow.
    for (const el of [...heads.slice(0, -1), cells[0], cells[3]]) {
      expect(has(el, 'break-words'), `<${el.tagName.toLowerCase()}> "${el.className}"`).toBe(true)
      expect(has(el, 'wrap-anywhere'), `<${el.tagName.toLowerCase()}> "${el.className}"`).toBe(false)
    }
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
      sharedFilesystem: false, requiredTotalBytes: 0, spaceChecked: false,
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

  // The daemon's `version` field has THREE states, not two (Go:
  // migrate.ExistingVolume.Version): a real PG_VERSION, the "empty" sentinel,
  // and "" for a dependency whose data carries no version marker AT ALL —
  // RabbitMQ, ZooKeeper. A renderer that knows only the first two printed
  // "PostgreSQL " with a blank version on a RabbitMQ volume holding 244 KB of
  // real data: the wrong product and a version the data never claimed. The
  // third arm may not be folded into "empty" either — "no cluster in it" is a
  // claim about PostgreSQL data, and that volume is not empty. Each arm is
  // pinned as the WHOLE sentence, so collapsing any two of them fails here.
  it.each([
    {
      what: 'a version the data claims', id: 'postgres', volume: 'postgres3',
      version: '17', bytes: 1024,
      text: 'Delete the existing volume postgres3 (1 KB, PostgreSQL 17) and recreate it',
    },
    {
      what: 'the "empty" sentinel', id: 'postgres', volume: 'postgres3',
      version: 'empty', bytes: 1024,
      text: 'Delete the existing volume postgres3 (1 KB, no cluster in it) and recreate it',
    },
    {
      what: 'data that carries no version marker', id: 'rabbitmq', volume: 'rabbitmq3',
      version: '', bytes: 244 * 1024,
      text: 'Delete the existing volume rabbitmq3 (244 KB) and recreate it',
    },
  ])('describes an existing target volume holding $what', async ({ id, volume, version, bytes, text }) => {
    mockDeps({
      items: [{
        ...items[0], id, app: id,
        currentImage: `${id}:1`, currentVersion: '1',
        targetImage: `${id}:2`, targetVersion: '2',
        status: 'upgrade-available', migratable: true,
      }],
    })
    vi.mocked(getDependencyPreflight).mockResolvedValue({
      ...okPreflight,
      existingTargetVolume: { name: volume, sizeBytes: bytes, version },
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /^upgrade$/i }))
    const label = await screen.findByText(new RegExp(volume))
    expect(label.textContent).toBe(text)
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

  // ...and it must not reappear as a step once the plan does. "preparing" is a
  // pseudo-step: it belongs to no plan, and it is the step the store records as
  // FINISHED the moment the first real one arrives. The unknown-id append then
  // put it after the last step of the plan, so a running RabbitMQ migration
  // showed a green-ticked "Preparing (checking the data and free space)" BELOW
  // "Stopping the new version" — a twelfth, completed step of an eleven-step
  // plan. The pre-plan state has its own spinner line; it is never a row.
  it('never renders the preparing pseudo-step in the step list', async () => {
    render(<DependenciesDialog open onClose={() => {}} />)
    await screen.findByTestId('dep-postgres')
    // The real sequence: a start with no plan, the daemon's own "preparing"
    // progress event, then the plan's first step.
    act(() => {
      useDepsStore.getState().onStart('rabbitmq', 0)
      useDepsStore.getState().onProgress({ appName: 'rabbitmq', phase: 'preparing', current: 0, total: 0, percent: 0, after: '' })
      useDepsStore.getState().onProgress({ appName: 'rabbitmq', phase: 'stop-new', current: 11, total: 11, percent: 0, after: '' })
    })
    await screen.findByTestId('deps-step-stop-new')
    expect(useDepsStore.getState().migration!.done).toContain('preparing')
    expect(screen.queryByTestId('deps-step-preparing')).toBeNull()
    // The list is exactly the plan — nothing appended, nothing dropped.
    expect(screen.getAllByTestId(/^deps-step-/).map((li) => li.getAttribute('data-testid'))).toEqual([
      'deps-step-stop-namespace', 'deps-step-pull-image', 'deps-step-create-volume',
      'deps-step-copy-volume', 'deps-step-start-old', 'deps-step-pre-upgrade',
      'deps-step-stop-old', 'deps-step-start-new', 'deps-step-post-upgrade',
      'deps-step-verify', 'deps-step-stop-new',
    ])
    // Not by its label either (asserted verbatim: `deps.step.pre-upgrade` is
    // "Preparing the data for the new version", so /preparing/i is not a test).
    expect(screen.getByTestId('deps-progress').textContent)
      .not.toContain('Preparing (checking the data and free space)')
  })

  // A ladder repeats step ids: pre-upgrade/start-new/post-upgrade once per
  // rung, and — critically — an INTERMEDIATE rung's own node-stop shares the
  // id "stop-new" with the plan's FINAL cleanup step, which sits at the last
  // position of the known vocabulary. A client that locates a row by
  // `ids.indexOf(migration.step)` finds that id's ONLY listed position (the
  // LAST one) and marks every row before it done — the data-integrity
  // `verify` step included — the moment the FIRST rung's own stop-new runs,
  // 8 steps and two more rungs before verify has executed even once. The
  // fix is to position a row by StepIndex/StepCount against the plan's REAL
  // step list (StepIDs, published by the daemon once the plan is built),
  // never by matching an id.
  it('never marks verify done before it has run, on a multi-rung ladder', async () => {
    render(<DependenciesDialog open onClose={() => {}} />)
    await screen.findByTestId('dep-postgres')
    // The real 19-step plan a 3-rung RabbitMQ ladder produces (captured
    // verbatim from BuildCopyUpgrade's own step list — see
    // internal/deps/migrate/copy_upgrade_ladder_test.go).
    const ladderSteps = [
      'stop-namespace', 'pull-image', 'create-volume', 'copy-volume', 'start-old',
      'pre-upgrade', 'stop-old', 'start-new', 'post-upgrade',
      'pre-upgrade', 'stop-new', 'start-new', 'post-upgrade',
      'pre-upgrade', 'stop-new', 'start-new', 'post-upgrade',
      'verify', 'stop-new',
    ]
    act(() => {
      useDepsStore.getState().onStart('rabbitmq', ladderSteps.length, undefined, ladderSteps)
      // The FIRST rung's own stop — an INTERMEDIATE one, position 11 of 19 —
      // not the plan's final cleanup step.
      useDepsStore.getState().onProgress({
        appName: 'rabbitmq', phase: 'stop-new', current: 11, total: 19, percent: 0, after: '',
      })
    })
    let verify = await screen.findByTestId('deps-step-verify')
    expect(verify).toHaveAttribute('data-state', 'todo')

    // The SECOND rung's own stop — position 15 of 19 — must not mark it
    // done either.
    act(() => useDepsStore.getState().onProgress({
      appName: 'rabbitmq', phase: 'stop-new', current: 15, total: 19, percent: 0, after: '',
    }))
    expect(screen.getByTestId('deps-step-verify')).toHaveAttribute('data-state', 'todo')

    // verify itself, running — position 18.
    act(() => useDepsStore.getState().onProgress({
      appName: 'rabbitmq', phase: 'verify', current: 18, total: 19, percent: 0, after: '',
    }))
    verify = screen.getByTestId('deps-step-verify')
    expect(verify).toHaveAttribute('data-state', 'active')

    // Only the plan's FINAL stop-new (position 19, after verify) may mark it
    // done.
    act(() => useDepsStore.getState().onProgress({
      appName: 'rabbitmq', phase: 'stop-new', current: 19, total: 19, percent: 0, after: '',
    }))
    expect(screen.getByTestId('deps-step-verify')).toHaveAttribute('data-state', 'done')
  })

  // The other half of the same defect: `pre-upgrade` runs once per rung, so
  // once rung 1's occurrence finishes it must not read as checked off FOREVER
  // — rungs 2 and 3 run that exact work again, on their own images, and a
  // checkmark sitting on it while it runs a second and third time is exactly
  // as misleading as `verify` ticking early. Both occurrences share one id;
  // only their POSITION in the plan tells them apart.
  it('does not mark a later rung of a repeated step done just because an earlier rung finished it', async () => {
    render(<DependenciesDialog open onClose={() => {}} />)
    await screen.findByTestId('dep-postgres')
    const ladderSteps = [
      'stop-namespace', 'pull-image', 'create-volume', 'copy-volume', 'start-old',
      'pre-upgrade', 'stop-old', 'start-new', 'post-upgrade',
      'pre-upgrade', 'stop-new', 'start-new', 'post-upgrade',
      'pre-upgrade', 'stop-new', 'start-new', 'post-upgrade',
      'verify', 'stop-new',
    ]
    act(() => {
      useDepsStore.getState().onStart('rabbitmq', ladderSteps.length, undefined, ladderSteps)
      // Rung 1's own pre-upgrade, running — position 6 of 19.
      useDepsStore.getState().onProgress({
        appName: 'rabbitmq', phase: 'pre-upgrade', current: 6, total: 19, percent: 0, after: '',
      })
    })
    expect(screen.getByTestId('deps-step-pre-upgrade')).toHaveAttribute('data-state', 'active')

    // Rung 1 finishes and rung 2's OWN pre-upgrade starts — position 10. The
    // first occurrence (rung 1's) is now genuinely done; the second (rung
    // 2's, same id) is the one running now, not a rerun of a finished row.
    act(() => useDepsStore.getState().onProgress({
      appName: 'rabbitmq', phase: 'pre-upgrade', current: 10, total: 19, percent: 0, after: '',
    }))
    expect(screen.getByTestId('deps-step-pre-upgrade')).toHaveAttribute('data-state', 'done')
    expect(screen.getByTestId('deps-step-pre-upgrade-9')).toHaveAttribute('data-state', 'active')
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
  // The copy-upgrade plan (RabbitMQ, ZooKeeper) is ELEVEN steps and shares
  // only four ids with PostgreSQL's ten. One hardcoded list rendered a dump
  // and a restore for a migration that does neither, and hid the copy that is
  // the whole operation.
  it('renders the copy plan for rabbitmq and the dump plan for postgres', async () => {
    render(<DependenciesDialog open onClose={() => {}} />)
    await screen.findByTestId('dep-postgres')

    act(() => {
      useDepsStore.getState().onStart('rabbitmq', 11)
      useDepsStore.getState().onProgress({ appName: 'rabbitmq', phase: 'copy-volume', current: 4, total: 11, percent: 0, after: '' })
    })
    expect(screen.getAllByTestId(/^deps-step-/).map((li) => li.getAttribute('data-testid'))).toEqual([
      'deps-step-stop-namespace', 'deps-step-pull-image', 'deps-step-create-volume',
      'deps-step-copy-volume', 'deps-step-start-old', 'deps-step-pre-upgrade',
      'deps-step-stop-old', 'deps-step-start-new', 'deps-step-post-upgrade',
      'deps-step-verify', 'deps-step-stop-new',
    ])
    // Every one of them is a real locale key, never the bare lookup key.
    for (const li of screen.getAllByTestId(/^deps-step-/)) {
      expect(li.textContent).not.toMatch(/^deps\.step\./)
    }

    act(() => {
      useDepsStore.getState().onStart('postgres', 10)
      useDepsStore.getState().onProgress({ appName: 'postgres', phase: 'dump', current: 4, total: 10, percent: 0, after: '' })
    })
    expect(screen.getAllByTestId(/^deps-step-/).map((li) => li.getAttribute('data-testid'))).toEqual([
      'deps-step-stop-namespace', 'deps-step-pull-image', 'deps-step-start-source',
      'deps-step-dump', 'deps-step-stop-source', 'deps-step-create-volume',
      'deps-step-start-target', 'deps-step-restore', 'deps-step-verify', 'deps-step-stop-target',
    ])
  })

  // Neither of these is "update the launcher": a vendor-forbidden hop is not
  // lifted by a newer launcher, and a bundle that offers something OLDER is
  // not an upgrade being held back at all. Both carry the daemon's own
  // sentence, which is the only part that says what to do.
  it('renders a blocked hop and a backwards bundle with their detail and no launcher hint', async () => {
    mockDeps({
      items: [
        {
          id: 'rabbitmq', app: 'rabbitmq', currentImage: 'rabbitmq:4.1.8-management', currentVersion: '4.1.8',
          targetImage: 'rabbitmq:4.3.5-management', targetVersion: '4.3.5',
          status: 'upgrade-blocked', migratable: true,
          statusDetail: 'RabbitMQ does not support 4.1 → 4.3 in one step: upgrade to 4.2 first.',
        },
        {
          id: 'postgres', app: 'postgres', currentImage: 'postgres:18.6', currentVersion: '18.6',
          targetImage: 'postgres:17.5', targetVersion: '17.5',
          status: 'bundle-older', migratable: true,
          statusDetail: 'the bundle offers postgres:17.5, which is older than the postgres:18.6 this namespace’s data runs on.',
        },
      ],
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    const blocked = await screen.findByTestId('dep-rabbitmq')
    expect(blocked).toHaveTextContent('upgrade to 4.2 first')
    expect(blocked.textContent).not.toMatch(/deps\.status\./)
    expect(blocked.textContent).not.toMatch(/citeck update/i)

    const older = screen.getByTestId('dep-postgres')
    expect(older).toHaveTextContent('older than the postgres:18.6')
    expect(older.textContent).not.toMatch(/deps\.status\./)
    expect(older.textContent).not.toMatch(/citeck update/i)
    // Neither is an upgrade: no Upgrade button anywhere on the list.
    expect(screen.queryByRole('button', { name: /^upgrade$/i })).toBeNull()
  })

  // A copy upgrade writes NOTHING to the host, so requiredHostBytes is 0 on a
  // preflight that measured everything it needed to. Reading that zero as
  // "unmeasured" hid the one requirement that exists; printing the host line
  // anyway claims a second requirement of 0 B.
  it('renders the volume requirement of a plan that writes nothing to the host', async () => {
    vi.mocked(getDependencyPreflight).mockResolvedValue({
      ...okPreflight, from: 'rabbitmq:4.1.8-management', to: 'rabbitmq:4.2.9-management',
      dataSizeBytes: 10, requiredHostBytes: 0, requiredVolumeBytes: 30,
      freeHostBytes: 0, freeVolumeBytes: 100, spaceChecked: true,
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /^upgrade$/i }))
    expect(await screen.findByText(/Data size:/)).toBeInTheDocument()
    expect(screen.getByText(/Volumes:/)).toHaveTextContent('30 B')
    expect(screen.queryByText(/Host \(dump\):/)).toBeNull()
  })

  // The rollback is a per-row action, and the row that cannot take it says why
  // instead of offering a button whose only answer is a refusal. The launcher
  // itself tells the operator they may reclaim the retained volume, so "it is
  // gone" is a state it actively creates.
  it('offers a rollback per row and shows the problem instead when it is unavailable', async () => {
    mockDeps({ items: [migratedPostgres] })
    render(<DependenciesDialog open onClose={() => {}} />)
    const row = await screen.findByTestId('dep-postgres')
    expect(screen.getByRole('button', { name: /roll back to 17\.5/i })).toBeEnabled()
    expect(row.textContent).not.toMatch(/deps\.rollback\./)

    mockDeps({
      items: [{
        ...migratedPostgres,
        rollback: {
          ...migratedPostgres.rollback, available: false,
          problem: 'volume postgres2 is gone, so there is no postgres data from postgres:17.5 to go back to',
        },
      }],
    })
    act(() => { useDepsStore.setState({ result: { id: 'postgres', success: true, message: '', at: Date.now(), kind: '' } }) })
    await waitFor(() => expect(screen.queryByRole('button', { name: /roll back to/i })).toBeNull())
    expect(screen.getByTestId('dep-postgres')).toHaveTextContent('volume postgres2 is gone')
  })

  // What the confirm screen adds to the daemon's own warnings: BOTH volumes as
  // fields the user can read at a glance, and the date — which the preflight
  // deliberately cannot supply, having no migration result to read it from.
  // Asserted inside the field list, not on the whole body: the warnings name
  // the same volumes, so a body-level match would pass with the fields gone.
  it('names both volumes and the migration date as fields of its own', async () => {
    mockDeps({ items: [migratedPostgres] })
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /roll back to 17\.5/i }))
    const fields = await screen.findByTestId('deps-rollback-fields')
    expect(fields).toHaveTextContent('17.5')
    expect(fields).toHaveTextContent('postgres2')
    expect(fields).toHaveTextContent('postgres3')
    expect(fields).toHaveTextContent('09.09.26')
    expect(fields.textContent).not.toMatch(/deps\.rollback\./)
    // Nothing about disk space: a rollback creates nothing and measures nothing.
    expect(screen.queryByText(/Data size:/)).toBeNull()
    expect(screen.queryByText(/Volumes:/)).toBeNull()
    expect(getDependencyRollbackPreflight).toHaveBeenCalledWith('postgres')
  })

  // A result slot that no longer holds the migration being undone answers 0.
  // A labelled blank is only noise, and the warnings still say "as it was when
  // the migration finished", which is true without a date.
  it('omits the date field when the result slot no longer holds the migration', async () => {
    mockDeps({
      items: [{ ...migratedPostgres, rollback: { ...migratedPostgres.rollback, migratedAt: 0 } }],
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /roll back to 17\.5/i }))
    const fields = await screen.findByTestId('deps-rollback-fields')
    expect(fields).toHaveTextContent('postgres2')
    expect(within(fields).queryByText(/Migrated on/i)).toBeNull()
  })

  // The daemon is the single source for what a rollback DOES, and it says it
  // through the preflight's warnings — the same channel that already carries
  // the existing-volume warning, the deferred deprecated-features check and
  // this one. Every warning is rendered, verbatim: dropping any of them by
  // position would make the dialog correct only for as long as the Go builder
  // emitted exactly the warnings it emits today.
  it('renders every rollback preflight warning verbatim', async () => {
    mockDeps({ items: [migratedPostgres] })
    vi.mocked(getDependencyRollbackPreflight).mockResolvedValue({
      ...okRollbackPreflight,
      warnings: ['image postgres:17.5 is not present locally; the rollback will pull it', ...rollbackConsequences],
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /roll back to 17\.5/i }))
    const body = await screen.findByTestId('deps-rollback-confirm')
    for (const w of ['image postgres:17.5 is not present locally', ...rollbackConsequences]) {
      expect(body).toHaveTextContent(w)
    }
  })

  // A refused rollback preflight blocks the button and says why.
  it('blocks the rollback on a refused preflight', async () => {
    mockDeps({ items: [migratedPostgres] })
    vi.mocked(getDependencyRollbackPreflight).mockResolvedValue({
      ...okRollbackPreflight, ok: false, warnings: [],
      problems: ['volume postgres2 holds PostgreSQL "18", not the cluster postgres:17.5 names'],
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /roll back to 17\.5/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent('not the cluster postgres:17.5 names')
    expect(screen.getByRole('button', { name: /^roll back$/i })).toBeDisabled()
    expect(postDependencyRollback).not.toHaveBeenCalled()
  })

  // Three steps on the migration's own progress channel (no second rendering
  // path), but they are the ROLLBACK's three — the ten of a dump plan would be
  // a list of things that will never happen.
  it('starts a rollback and follows it on its own three steps', async () => {
    mockDeps({ items: [migratedPostgres] })
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /roll back to 17\.5/i }))
    await screen.findByTestId('deps-rollback-confirm')
    fireEvent.click(screen.getByRole('button', { name: /^roll back$/i }))
    await waitFor(() => expect(postDependencyRollback).toHaveBeenCalledWith('postgres'))

    act(() => useDepsStore.getState().onProgress({
      appName: 'postgres', phase: 'switch-generation', current: 2, total: 3, percent: 0, after: '',
    }))
    const progress = await screen.findByTestId('deps-progress')
    expect(screen.getAllByTestId(/^deps-step-/).map((li) => li.getAttribute('data-testid'))).toEqual([
      'deps-step-stop-namespace', 'deps-step-switch-generation', 'deps-step-start-namespace',
    ])
    expect(progress.textContent).not.toMatch(/deps\.(step|progress)\./)
    // The title says it is a rollback, not a migration.
    expect(progress).toHaveTextContent(/roll/i)
  })

  // One result slot, two operations: "postgres 18.6 → 17.5 succeeded" reads as
  // a migration to an older version unless the verdict says which it was.
  it('reports a rolled-back verdict as a rollback, not as a migration', async () => {
    mockDeps({ items: [migratedPostgres] })
    render(<DependenciesDialog open onClose={() => {}} />)
    await screen.findByTestId('dep-postgres')
    act(() => useDepsStore.getState().onStart('postgres', 3, 'rollback'))
    await screen.findByTestId('deps-progress')
    act(() => useDepsStore.getState().onComplete('postgres', 'postgres rolled back to postgres:17.5'))
    const result = await screen.findByTestId('deps-result')
    expect(result.textContent).not.toMatch(/migrated/i)
    expect(result.textContent).not.toMatch(/deps\.result\./)
    expect(result).toHaveTextContent(/rolled/i)

    act(() => useDepsStore.getState().onStart('postgres', 3, 'rollback'))
    await screen.findByTestId('deps-progress')
    act(() => useDepsStore.getState().onError('postgres', 'stop namespace: boom'))
    const failed = await screen.findByTestId('deps-result')
    expect(failed).toHaveTextContent('stop namespace: boom')
    // The migration's "everything was rolled back" line would be nonsense here.
    expect(failed.textContent).not.toMatch(/runs on the previous version/i)
  })

  // ZooKeeper 3.8 and 3.9 share one on-disk format: the copy is insurance, not
  // a conversion, and saying so is the honest version of a screen that
  // otherwise implies a data migration.
  it('explains the ZooKeeper copy on its own confirm screen', async () => {
    mockDeps({
      items: [{
        id: 'zookeeper', app: 'zookeeper', currentImage: 'zookeeper:3.8.4', currentVersion: '3.8.4',
        targetImage: 'zookeeper:3.9.5', targetVersion: '3.9.5', status: 'upgrade-available', migratable: true,
      }],
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /^upgrade$/i }))
    const intro = await screen.findByTestId('deps-confirm')
    expect(intro).toHaveTextContent(/data volume is copied/i)
    expect(intro).toHaveTextContent(/container swap/i)
    expect(intro.textContent).not.toMatch(/deps\.(confirm|zk)\./)
  })
  // `oldVolume` is one field with two opposite meanings: after a MIGRATION it
  // is the old data the user may reclaim, after a ROLLBACK it is the newer
  // data that just became unreachable. Telling a user to go and delete the
  // latter from the Volumes page is the worst sentence this dialog could say.
  it('names the frozen volume as kept-and-never-read, not as space to reclaim', async () => {
    mockDeps({
      items: [migratedPostgres],
      lastResult: {
        id: 'postgres', from: 'postgres:18.6', to: 'postgres:17.5', finishedAt: 1,
        success: true, oldVolume: 'postgres3', kind: 'rollback',
      },
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    await screen.findByTestId('dep-postgres')
    act(() => useDepsStore.getState().onStart('postgres', 3, 'rollback'))
    await screen.findByTestId('deps-progress')
    act(() => useDepsStore.getState().onComplete('postgres', 'postgres rolled back to postgres:17.5'))
    const result = await screen.findByTestId('deps-result')
    expect(result).toHaveTextContent('postgres3')
    expect(result).toHaveTextContent(/never reads it again/i)
    expect(result.textContent).not.toMatch(/Volumes page/i)
    // A rollback this client did not click reads its target off the one result
    // slot rather than naming nothing.
    expect(result).toHaveTextContent('postgres:17.5')
  })

  // The daemon refuses a rollback with a 409 while a journal is open or any
  // long operation is held, so a live button could only ever raise an error
  // modal — the same rule the Upgrade button already follows.
  it('disables the rollback action while the daemon would refuse it', async () => {
    mockDeps({ items: [migratedPostgres] })
    render(<DependenciesDialog open onClose={() => {}} />)
    await screen.findByTestId('dep-postgres')
    expect(screen.getByRole('button', { name: /roll back to 17\.5/i })).toBeEnabled()
    act(() => useDepsStore.setState({ rollbackPending: 'a previous migration of postgres left a rollback pending' }))
    expect(screen.getByRole('button', { name: /roll back to 17\.5/i })).toBeDisabled()
  })
  // A namespace has ONE result slot and it may hold a rollback of a DIFFERENT
  // dependency (migrating or rolling back a second one replaces it). Reading
  // the target off it unconditionally would report this dependency as having
  // gone back to the other one's version.
  it("never names another dependency's rollback target on this one's verdict", async () => {
    mockDeps({
      items: [migratedPostgres],
      lastResult: {
        id: 'rabbitmq', from: 'rabbitmq:4.2.9-management', to: 'rabbitmq:4.1.8-management',
        finishedAt: 1, success: true, kind: 'rollback',
      },
    })
    render(<DependenciesDialog open onClose={() => {}} />)
    await screen.findByTestId('dep-postgres')
    act(() => useDepsStore.getState().onStart('postgres', 3, 'rollback'))
    await screen.findByTestId('deps-progress')
    act(() => useDepsStore.getState().onComplete('postgres', 'postgres rolled back'))
    const result = await screen.findByTestId('deps-result')
    expect(result.textContent).not.toMatch(/rabbitmq/)
    // With no target it can trust, it says so without naming a version.
    expect(result).toHaveTextContent(/previous version/i)
  })
})
