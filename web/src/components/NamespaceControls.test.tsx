import { render, screen, waitFor, act } from '@testing-library/react'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { NamespaceControls } from './NamespaceControls'
import { useDashboardStore } from '../lib/store'
import type { NamespaceDto } from '../lib/types'
import { postNamespaceStart } from '../lib/api'
import { showError } from '../lib/errorModal'

vi.mock('../lib/api', () => ({
  postNamespaceStart: vi.fn().mockResolvedValue({ success: true }),
  postNamespaceStop: vi.fn().mockResolvedValue({ success: true }),
}))

// The pre-start registry-credentials gate is orthogonal to which action the
// button dispatches; let it pass straight through.
vi.mock('./RegistryPreflight', () => ({
  useRegistryPreflight: () => ({
    preflight: (run: () => void | Promise<void>) => run(),
    dialog: null,
  }),
}))

vi.mock('../lib/toast', () => ({ toast: vi.fn() }))
vi.mock('../lib/errorModal', () => ({ showError: vi.fn() }))

beforeEach(() => {
  vi.mocked(postNamespaceStart).mockClear()
  vi.mocked(showError).mockClear()
  useDashboardStore.setState({ namespace: null })
})

function setNamespace(patch: Partial<NamespaceDto>) {
  useDashboardStore.setState({
    namespace: { id: 'n', name: 'n', status: 'STOPPED', bundleRef: '', apps: [], ...patch } as NamespaceDto,
  })
}

function clickPrimary() {
  // Primary (Update&Start) is the first button in the split control.
  screen.getAllByRole('button')[0].click()
}

describe('NamespaceControls primary action', () => {
  // Kotlin 1.x NamespaceRuntime.runtimeThreadAction() handles StartNsCmd with
  // NO branch on nsStatus: a running namespace takes the identical path as a
  // stopped one (throttled git pull -> generateNs -> re-drive every app).
  // Routing the running case to /namespace/reload instead dropped
  // refreshImages, so the :snapshot digest refresh never ran, every app hash
  // stayed identical and the click was a visible no-op.
  it.each(['RUNNING', 'STALLED'])(
    'dispatches update-and-start (not reload) when namespace is %s',
    async (status) => {
      render(<NamespaceControls status={status} />)
      clickPrimary()
      await waitFor(() => expect(postNamespaceStart).toHaveBeenCalledWith(false))
    },
  )

  it('dispatches update-and-start when namespace is STOPPED', async () => {
    render(<NamespaceControls status="STOPPED" />)
    clickPrimary()
    await waitFor(() => expect(postNamespaceStart).toHaveBeenCalledWith(false))
  })
})

// Nothing about namespace/app status changes while the daemon waits on
// reloadMu, pulls git and regenerates — so without a dedicated signal the click
// reads as ignored for as long as that takes. The button must acknowledge it
// immediately and stay acknowledged for the whole stretch.
describe('NamespaceControls click feedback', () => {
  it('shows the updating state before the request has even resolved', async () => {
    // The request is held open deliberately. Awaiting the click instead would
    // flush the resolved promise first, and then the assertions could not tell
    // an echo set BEFORE `await actionFns[a]()` from one set after it — i.e. the
    // test would still pass with the whole point of the local echo deleted.
    let release: () => void = () => {}
    vi.mocked(postNamespaceStart).mockImplementationOnce(
      () => new Promise((resolve) => { release = () => { resolve({ success: true, message: '' }) } }),
    )

    render(<NamespaceControls status="STOPPED" />)
    const btn = screen.getAllByRole('button')[0]
    expect(btn).not.toBeDisabled()

    act(() => { btn.click() })

    // Request still in flight, no SSE, no refetch — only the local echo can be
    // driving this.
    expect(screen.getByTitle('Updating…')).toBeDefined()
    expect(screen.getAllByRole('button')[0]).toBeDisabled()

    await act(async () => { release() })
  })

  it('keeps the updating state from the daemon flag while status is still STOPPED', () => {
    setNamespace({ status: 'STOPPED', updating: true })
    render(<NamespaceControls status="STOPPED" />)

    // No click in this window at all (e.g. a second desktop window, or a page
    // reload mid-update): the server flag alone must drive the indicator.
    expect(screen.getByTitle('Updating…')).toBeDefined()
    expect(screen.getAllByRole('button')[0]).toBeDisabled()
  })

  it('returns to the normal label once the daemon lowers the flag', () => {
    setNamespace({ status: 'RUNNING', updating: false })
    render(<NamespaceControls status="RUNNING" />)

    expect(screen.getByTitle('Update & Start')).toBeDefined()
    expect(screen.getAllByRole('button')[0]).not.toBeDisabled()
  })

  // The daemon can finish a pass in microseconds — both skip paths and a fast
  // doReloadEx error do — so `updating` may never be observed as true and the
  // status never moves. The post-click refetch has to be what releases the echo;
  // relying on the 30s backstop leaves the button spinning and dead for half a
  // minute, which is worse than the "click did nothing" this all replaces.
  it('releases the echo when the post-click refetch lands, not on the 30s backstop', async () => {
    const originalFetch = useDashboardStore.getState().fetchData
    vi.useFakeTimers()
    try {
      const fetchData = vi.fn().mockResolvedValue(undefined)
      // `updating` stays false throughout: the pass already finished.
      useDashboardStore.setState({ fetchData, namespace: null })

      render(<NamespaceControls status="STOPPED" />)
      await act(async () => { screen.getAllByRole('button')[0].click() })
      expect(screen.getAllByRole('button')[0]).toBeDisabled()

      await act(async () => { await vi.advanceTimersByTimeAsync(600) })

      expect(fetchData).toHaveBeenCalled()
      expect(screen.getAllByRole('button')[0]).not.toBeDisabled()
      expect(screen.getByTitle('Update & Start')).toBeDefined()
    } finally {
      vi.useRealTimers()
      useDashboardStore.setState({ fetchData: originalFetch })
    }
  })

  // A pass that fails server-side leaves the UI identical to a successful one:
  // the spinner stops, nothing changed. That is the same "went nowhere" the
  // whole change removes, so it has to be shown — on the same modal the
  // synchronous failure path uses.
  it('shows the daemon-reported failure of an async pass, exactly once', () => {
    setNamespace({ status: 'STOPPED', updateError: 'git pull failed: dial tcp: timeout', updateErrorAt: 1234 })
    const { rerender } = render(<NamespaceControls status="STOPPED" />)

    expect(showError).toHaveBeenCalledTimes(1)
    expect(vi.mocked(showError).mock.calls[0][0]).toMatchObject({
      message: 'git pull failed: dial tcp: timeout',
    })

    // Same occurrence re-rendered (or the component remounted) must not re-raise
    // it — the failure stays in the DTO until the next click.
    rerender(<NamespaceControls status="STOPPED" />)
    render(<NamespaceControls status="STOPPED" />)
    expect(showError).toHaveBeenCalledTimes(1)
  })

  it('drops the echo when the request itself fails, so the button is usable again', async () => {
    vi.mocked(postNamespaceStart).mockRejectedValueOnce(new Error('boom'))
    render(<NamespaceControls status="STOPPED" />)

    await act(async () => { screen.getAllByRole('button')[0].click() })

    await waitFor(() => expect(screen.getAllByRole('button')[0]).not.toBeDisabled())
    expect(screen.getByTitle('Update & Start')).toBeDefined()
  })
})
