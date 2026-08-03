import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import App from './App'

// Dashboard store stand-in — MainLayout reads `namespace` (route guards),
// `health` (Docker takeover screen) and `fetchData`. `namespace: null` is the
// boot window: the value the daemon hasn't answered for yet.
const store = vi.hoisted(() => {
  const state: { namespace: unknown; health: unknown; fetchData: () => void } = {
    namespace: null,
    health: null,
    fetchData: () => {},
  }
  const hook = (sel: (s: typeof state) => unknown) => sel(state)
  return { state, hook }
})

vi.mock('./lib/store', () => ({ useDashboardStore: store.hook }))
vi.mock('./lib/daemonStatus', () => ({
  useIsDesktop: () => true,
  useActiveWorkspaceId: () => 'ws-1',
  useDaemonStatusStore: { getState: () => ({ fetch: async () => ({}), refresh: async () => ({}) }) },
}))
vi.mock('./lib/desktop', () => ({
  primeDesktopModeCache: () => Promise.resolve(true),
  detectInstalledButStopped: () => false,
  isDesktopModeSync: () => true,
}))

// Route targets are stubbed — these tests assert WHICH route renders, not what
// each page draws. The settings page itself is deliberately left real.
vi.mock('./pages/Dashboard', () => ({ Dashboard: () => <div data-testid="dashboard" /> }))
vi.mock('./pages/Welcome', () => ({ Welcome: () => <div data-testid="welcome" /> }))
vi.mock('./components/SecretsUnlockGuard', () => ({ SecretsUnlockGuard: () => null }))
vi.mock('./components/AuthRequired', () => ({ AuthRequired: () => null }))
vi.mock('./components/UpdateNotification', () => ({ UpdateNotification: () => null }))
vi.mock('./components/WorkspaceSelector', () => ({ WorkspaceSelector: () => null }))

// Spread the real module so every transitive import resolves, then neutralize
// the calls that would hit the network on mount.
vi.mock('./lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./lib/api')>()),
  getHealth: vi.fn().mockResolvedValue({ healthy: true, checks: [] }),
  getDaemonStatus: vi.fn().mockResolvedValue({ desktop: true, workspace: 'ws-1' }),
  getRegistryBindings: vi.fn().mockResolvedValue({}),
  getMissingRegistryAuth: vi.fn().mockResolvedValue([]),
  getSecrets: vi.fn().mockResolvedValue([]),
  listWorkspaces: vi.fn().mockResolvedValue([]),
  getMigrationStatus: vi.fn().mockResolvedValue({ encrypted: true, locked: false }),
  putUIPrefs: vi.fn().mockResolvedValue({}),
}))

beforeEach(() => {
  store.state.namespace = null
})

describe('App /config routing', () => {
  it('opens a rendered settings page from the gear with no namespace open', async () => {
    // The reported bug: gear → /config → hasNamespace guard → redirect to "/" →
    // Welcome again, where the gear was hidden. Pixel-identical page, no error.
    window.history.pushState({}, '', '/welcome')
    render(<App />)

    fireEvent.click(screen.getByTitle('Settings'))

    expect(await screen.findByRole('heading', { name: 'Settings' })).toBeInTheDocument()
    expect(screen.queryByTestId('welcome')).not.toBeInTheDocument()
    // ...and the affordance the user just used is still there.
    expect(screen.getByTitle('Settings')).toBeInTheDocument()
  })

  it('renders /config during the boot window (namespace still null)', async () => {
    // Even WITH a namespace configured, `namespace` is null until fetchData()
    // resolves — a fast click used to be swallowed by the same redirect.
    window.history.pushState({}, '', '/config')
    render(<App />)

    expect(await screen.findByRole('heading', { name: 'Settings' })).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByTestId('welcome')).not.toBeInTheDocument())
    expect(screen.queryByTestId('dashboard')).not.toBeInTheDocument()
  })
})
