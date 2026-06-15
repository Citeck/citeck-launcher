import { render, screen, act, waitFor } from '@testing-library/react'
import { describe, it, expect, beforeEach, beforeAll, vi } from 'vitest'
import { RegistryAuthBanner } from './RegistryAuthBanner'
import { useDashboardStore } from '../lib/store'

// The dialog/picker fire API calls on mount; stub the module so tests stay
// offline and deterministic.
vi.mock('../lib/api', () => ({
  getRegistryBindings: vi.fn().mockResolvedValue({}),
  setRegistryBinding: vi.fn().mockResolvedValue({ success: true }),
  postAppsRetryPullFailed: vi.fn().mockResolvedValue({ success: true }),
  getSecrets: vi.fn().mockResolvedValue([]),
  listWorkspaces: vi.fn().mockResolvedValue([]),
  deleteSecret: vi.fn().mockResolvedValue({ success: true }),
  createSecret: vi.fn().mockResolvedValue({ success: true }),
  updateSecret: vi.fn().mockResolvedValue({}),
}))

const HOST = 'enterprise-registry.citeck.ru'

beforeAll(() => {
  // jsdom doesn't implement <dialog> showModal/close — stub them.
  HTMLDialogElement.prototype.showModal = vi.fn()
  HTMLDialogElement.prototype.close = vi.fn()
})

beforeEach(() => {
  useDashboardStore.setState({ pullAuthRequired: {} })
})

describe('RegistryAuthBanner', () => {
  it('auto-opens the dialog and keeps it open across a pullAuthRequired blink', async () => {
    useDashboardStore.setState({ pullAuthRequired: { emodel: HOST } })
    render(<RegistryAuthBanner />)

    // Auto-opened: the dialog's explanation (unique phrase) is shown.
    await screen.findByText(/Pick an existing one/)
    expect(screen.getByText(/Registry credentials needed for/)).toBeInTheDocument()

    // The reconciler retry briefly clears the marker (app leaves PULL_FAILED).
    act(() => { useDashboardStore.setState({ pullAuthRequired: {} }) })

    // Banner is gone, but the dialog (and its nested create form) must NOT
    // unmount mid-edit — this is the regression being guarded.
    await waitFor(() => {
      expect(screen.queryByText(/Registry credentials needed for/)).not.toBeInTheDocument()
    })
    expect(screen.getByText(/Pick an existing one/)).toBeInTheDocument()
  })

  it('auto-opens a host only once (no re-pop after the marker blinks back)', async () => {
    useDashboardStore.setState({ pullAuthRequired: { emodel: HOST } })
    render(<RegistryAuthBanner />)
    await screen.findByText(/Pick an existing one/)

    // Blink off, then back on (failed pull re-emits) — must not re-pop a second
    // dialog instance.
    act(() => { useDashboardStore.setState({ pullAuthRequired: {} }) })
    act(() => { useDashboardStore.setState({ pullAuthRequired: { emodel: HOST } }) })

    // Still exactly one dialog explanation present.
    expect(screen.getAllByText(/Pick an existing one/)).toHaveLength(1)
  })
})
