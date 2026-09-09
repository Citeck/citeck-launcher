import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { describe, it, expect, beforeAll, beforeEach, vi } from 'vitest'
import { DependencyUpgradeBanner, ROLLBACK_POLL_MS } from './DependencyUpgradeBanner'
import { DependenciesDialog } from './DependenciesDialog'
import { useDashboardStore } from '../lib/store'
import { useDepsStore } from '../lib/depsStore'
import { useUpdateStore } from '../lib/updateStore'
import { isDesktopModeSync } from '../lib/desktop'
import { getDependencies } from '../lib/api'
import type { NamespaceDto } from '../lib/types'

vi.mock('../lib/api', () => ({
  getDependencies: vi.fn().mockResolvedValue({ items: [] }),
  // Reached only through the update dialog, which this suite never opens.
  getUpdateStatus: vi.fn(),
  checkUpdate: vi.fn(),
  // The dependencies dialog is rendered next to the banner by the shared-fetch
  // case; it never gets as far as a preflight there.
  getDependencyPreflight: vi.fn(),
  postDependencyMigrate: vi.fn(),
}))
vi.mock('../lib/errorModal', () => ({ showError: vi.fn() }))
vi.mock('../lib/desktop', () => ({ isDesktopModeSync: vi.fn(() => false) }))

function setUpgrades(upgrades: NamespaceDto['dependencyUpgrades']) {
  useDashboardStore.setState({
    namespace: { id: 'n', name: 'n', status: 'STOPPED', bundleRef: '', apps: [], dependencyUpgrades: upgrades } as NamespaceDto,
  })
}

const migratable = { id: 'postgres', app: 'postgres', from: 'postgres:17.5', to: 'postgres:18', migratable: true }
const needsLauncher = { id: 'rabbitmq', app: 'rabbitmq', from: 'rabbitmq:4.1.2-management', to: 'rabbitmq:4.2.9-management', migratable: false }

beforeAll(() => {
  // jsdom implements neither; the shared-fetch case renders the dialog.
  HTMLDialogElement.prototype.showModal = vi.fn(function (this: HTMLDialogElement) { this.open = true })
  HTMLDialogElement.prototype.close = vi.fn(function (this: HTMLDialogElement) { this.open = false })
})

beforeEach(() => {
  vi.mocked(getDependencies).mockClear()
  vi.mocked(getDependencies).mockResolvedValue({ items: [] })
  vi.mocked(isDesktopModeSync).mockReturnValue(false)
  useDepsStore.setState({ dismissedKey: null, data: null, migration: null, result: null, rollbackPending: '' })
  useUpdateStore.setState({ status: null })
  useDashboardStore.setState({ namespace: null })
})

describe('DependencyUpgradeBanner', () => {
  it('renders nothing without upgrades', async () => {
    setUpgrades([])
    const { container } = render(<DependencyUpgradeBanner onDetails={() => {}} />)
    await waitFor(() => expect(getDependencies).toHaveBeenCalled())
    expect(container).toBeEmptyDOMElement()
  })

  it('distinguishes migratable from launcher-update upgrades', () => {
    setUpgrades([migratable, needsLauncher])
    render(<DependencyUpgradeBanner onDetails={() => {}} />)
    const text = screen.getByRole('status').textContent ?? ''
    expect(text).toContain('postgres:18')
    expect(text).toContain('rabbitmq:4.2.9-management')
    expect(text).toMatch(/launcher/i)
  })

  it('dismisses for the current set and comes back when the set changes', () => {
    setUpgrades([migratable])
    const { rerender } = render(<DependencyUpgradeBanner onDetails={() => {}} />)
    fireEvent.click(screen.getByLabelText(/dismiss/i))
    expect(screen.queryByRole('status')).toBeNull()
    setUpgrades([{ ...migratable, to: 'postgres:19' }])
    rerender(<DependencyUpgradeBanner onDetails={() => {}} />)
    expect(screen.getByRole('status')).toBeInTheDocument()
  })

  // A pending rollback freezes the dependency's version and refuses every new
  // migration until it succeeds — it outranks an available upgrade, and hiding
  // it would leave the user with no sign of a namespace that cannot move.
  it('shows a pending rollback instead of the upgrade offer, and never lets it be dismissed', async () => {
    setUpgrades([migratable])
    useDepsStore.setState({ dismissedKey: 'postgres:postgres:18' })
    vi.mocked(getDependencies).mockResolvedValue({ items: [], rollbackPending: 'a previous migration of postgres left a rollback pending' })
    render(<DependencyUpgradeBanner onDetails={() => {}} />)
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('rollback pending')
    expect(screen.queryByLabelText(/dismiss/i)).toBeNull()
  })

  // The daemon's load-time recovery retries the rollback and, on success,
  // clears the journal — with NO deps_migration_* event and no result, and
  // `rollbackPending` is not part of NamespaceDto either. Nothing would ever
  // take the red banner down, and it is deliberately not dismissible, so while
  // the alarm is up the banner polls for it. Polling is what the state needs
  // rather than following namespace fetches: after a failed rollback the
  // namespace is NOT handed back running, so there are no app_stats ticks and
  // possibly no namespace traffic at all.
  it('takes the rollback notice down once a poll reports it cleared', async () => {
    vi.useFakeTimers()
    try {
      setUpgrades([])
      vi.mocked(getDependencies).mockResolvedValue({ items: [], rollbackPending: 'rollback pending' })
      render(<DependencyUpgradeBanner onDetails={() => {}} />)
      await act(async () => { await Promise.resolve() })
      expect(screen.getByRole('alert')).toBeInTheDocument()

      vi.mocked(getDependencies).mockResolvedValue({ items: [] })
      await act(async () => { await vi.advanceTimersByTimeAsync(ROLLBACK_POLL_MS) })
      expect(screen.queryByRole('alert')).toBeNull()
      // And the poll stops with the alarm it belongs to.
      const after = vi.mocked(getDependencies).mock.calls.length
      await act(async () => { await vi.advanceTimersByTimeAsync(ROLLBACK_POLL_MS * 3) })
      expect(vi.mocked(getDependencies).mock.calls.length).toBe(after)
    } finally {
      vi.useRealTimers()
    }
  })

  // `app_stats` fires every 5s per RUNNING app and `fetchData` publishes a new
  // namespace object each time. Following that identity while the alarm is up
  // meant one dependencies GET per running app per tick — on a 24-app stand,
  // several a second, forever, for a state that changes only across a daemon
  // restart. The poll is one request per interval regardless of the stand.
  it('does not fetch again for every namespace update while the alarm is up', async () => {
    vi.useFakeTimers()
    try {
      setUpgrades([])
      vi.mocked(getDependencies).mockResolvedValue({ items: [], rollbackPending: 'rollback pending' })
      render(<DependencyUpgradeBanner onDetails={() => {}} />)
      await act(async () => { await Promise.resolve() })
      const initial = vi.mocked(getDependencies).mock.calls.length

      // 20 app_stats ticks: exactly what the store does — a NEW namespace object.
      for (let i = 0; i < 20; i++) {
        await act(async () => { setUpgrades([]) })
      }
      expect(vi.mocked(getDependencies).mock.calls.length).toBe(initial)

      await act(async () => { await vi.advanceTimersByTimeAsync(ROLLBACK_POLL_MS) })
      expect(vi.mocked(getDependencies).mock.calls.length).toBe(initial + 1)
    } finally {
      vi.useRealTimers()
    }
  })

  // The banner and the dialog render the same payload. They also react to the
  // same triggers and are on screen together, so a verdict used to cost two
  // identical GETs; the store's in-flight dedupe makes it one.
  it('shares one dependencies fetch with the dialog on a verdict', async () => {
    setUpgrades([migratable])
    render(
      <>
        <DependencyUpgradeBanner onDetails={() => {}} />
        <DependenciesDialog open onClose={() => {}} />
      </>,
    )
    await waitFor(() => expect(getDependencies).toHaveBeenCalled())
    vi.mocked(getDependencies).mockClear()

    // The end of a migration is what moves the pin, the last result and the
    // rollback state — both surfaces go and look.
    await act(async () => { useDepsStore.getState().onComplete('postgres', 'done') })
    await waitFor(() => expect(getDependencies).toHaveBeenCalled())
    expect(getDependencies).toHaveBeenCalledTimes(1)
  })

  it('offers the update dialog on desktop and names the CLI command in the server web UI', () => {
    setUpgrades([needsLauncher])
    render(<DependencyUpgradeBanner onDetails={() => {}} />)
    expect(screen.getByRole('status').textContent).toContain('citeck update')
    expect(screen.queryByRole('button', { name: /update the launcher/i })).toBeNull()

    vi.mocked(isDesktopModeSync).mockReturnValue(true)
    useUpdateStore.setState({ status: { currentVersion: '2.11.7', latestVersion: '2.12.0', available: true, applying: false } })
    render(<DependencyUpgradeBanner onDetails={() => {}} />)
    expect(screen.getAllByRole('button', { name: /update the launcher/i }).length).toBeGreaterThan(0)
  })

  // The daemon now carries the pending rollback in NamespaceDto, so the alarm
  // arrives on the ordinary namespace fetch. It must not wait for a
  // dependencies GET: after a failed rollback the namespace is not handed back
  // running, and nothing on this screen asks again until a remount, a
  // namespace switch, or the banner's own poll — which only starts once the
  // alarm is already up.
  it('raises the alarm from the namespace fetch, with a dependencies payload that knows nothing about it', async () => {
    setUpgrades([migratable])
    vi.mocked(getDependencies).mockResolvedValue({ items: [] })
    render(<DependencyUpgradeBanner onDetails={() => {}} />)
    await waitFor(() => expect(getDependencies).toHaveBeenCalled())
    expect(screen.queryByRole('alert')).toBeNull()
    const calls = vi.mocked(getDependencies).mock.calls.length

    // What `fetchData` does with NamespaceDto.dependencyRollbackPending.
    act(() => useDepsStore.getState().setRollbackPending('a previous migration of postgres left a rollback pending'))
    expect(screen.getByRole('alert')).toHaveTextContent('rollback pending')
    // And nothing had to be re-fetched to learn it.
    expect(vi.mocked(getDependencies).mock.calls.length).toBe(calls)
  })

  it('opens the dependencies dialog from Details', () => {
    const onDetails = vi.fn()
    setUpgrades([migratable])
    render(<DependencyUpgradeBanner onDetails={onDetails} />)
    fireEvent.click(screen.getByRole('button', { name: /details/i }))
    expect(onDetails).toHaveBeenCalled()
  })
  // Three groups, not two. `migratable` is FALSE for a vendor-blocked pair as
  // well as for one this launcher has no plan for, so it cannot be the
  // discriminator: without `blocked` the banner promised "update the launcher"
  // for a hop no launcher will ever take.
  it('splits a vendor-blocked hop from the ones a newer launcher would fix', () => {
    setUpgrades([
      migratable,
      { id: 'zookeeper', app: 'zookeeper', from: 'zookeeper:3.8.4', to: 'zookeeper:3.9.5', migratable: false },
      {
        id: 'rabbitmq', app: 'rabbitmq', from: 'rabbitmq:4.1.8-management', to: 'rabbitmq:4.3.5-management',
        migratable: false, blocked: 'RabbitMQ does not support 4.1 → 4.3 in one step: upgrade to 4.2 first.',
      },
    ])
    render(<DependencyUpgradeBanner onDetails={() => {}} />)
    const text = screen.getByRole('status').textContent ?? ''
    expect(text).toMatch(/postgres postgres:17\.5 → postgres:18/)
    expect(text).toMatch(/zookeeper zookeeper:3\.8\.4 → zookeeper:3\.9\.5/)
    expect(text).toMatch(/rabbitmq rabbitmq:4\.1\.8-management → rabbitmq:4\.3\.5-management/)
    expect(text).not.toMatch(/deps\.banner\./)
    // The blocked one is NOT in the "a newer launcher would fix this" clause.
    const needLauncher = text.slice(text.indexOf('zookeeper zookeeper'))
    expect(needLauncher).not.toMatch(/rabbitmq/)
  })

  // A bundle offering something OLDER than the data is not an upgrade: there
  // is nothing to migrate and nothing to wait for, so advertising it would
  // make the user open a dialog to learn there is nothing to do.
  it('leaves a backwards hold out of the banner entirely', () => {
    setUpgrades([{
      id: 'postgres', app: 'postgres', from: 'postgres:18.6', to: 'postgres:17.5',
      migratable: false, bundleOlder: true,
    }])
    const { container } = render(<DependencyUpgradeBanner onDetails={() => {}} />)
    expect(container).toBeEmptyDOMElement()
  })

  // ...and it must not resurrect a dismissed banner either: the dismissal is
  // keyed by what is on OFFER, and a backwards hold is not on offer.
  it('does not re-raise a dismissed banner because a backwards hold appeared', () => {
    setUpgrades([migratable])
    const { rerender } = render(<DependencyUpgradeBanner onDetails={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: /dismiss/i }))
    expect(screen.queryByRole('status')).toBeNull()

    setUpgrades([
      migratable,
      { id: 'postgres', app: 'postgres', from: 'postgres:18.6', to: 'postgres:17.5', migratable: false, bundleOlder: true },
    ])
    rerender(<DependencyUpgradeBanner onDetails={() => {}} />)
    expect(screen.queryByRole('status')).toBeNull()
  })
})
