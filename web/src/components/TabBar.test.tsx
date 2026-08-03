import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { MemoryRouter, useLocation } from 'react-router'
import { TabBar } from './TabBar'
import type { NamespaceDto } from '../lib/types'

// Minimal stand-in for the dashboard zustand store: TabBar only ever reads
// `namespace` out of it, and these tests drive that field directly. A real
// store would drag in lib/api + the SSE machinery for one boolean.
const store = vi.hoisted(() => {
  const state: { namespace: unknown } = { namespace: null }
  const hook = (sel: (s: typeof state) => unknown) => sel(state)
  return { state, hook }
})

vi.mock('../lib/store', () => ({ useDashboardStore: store.hook }))
vi.mock('../lib/daemonStatus', () => ({
  useIsDesktop: () => true,
  useActiveWorkspaceId: () => 'ws-1',
  useDaemonStatusStore: { getState: () => ({ refresh: vi.fn() }) },
}))
// Both hit the network on mount and are irrelevant to the gear's routing.
vi.mock('./WorkspaceSelector', () => ({ WorkspaceSelector: () => null }))
vi.mock('./UpdateNotification', () => ({ UpdateNotification: () => null }))

const NS = { id: 'default', name: 'Default', bundleRef: 'community/2.0' } as unknown as NamespaceDto

function LocationProbe() {
  const location = useLocation()
  return <div data-testid="loc">{location.pathname}</div>
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <TabBar />
      <LocationProbe />
    </MemoryRouter>,
  )
}

const gear = () => screen.getByTitle('Settings')

beforeEach(() => {
  store.state.namespace = null
})

describe('TabBar settings gear', () => {
  it('renders on the Welcome screen at "/" (no namespace open)', () => {
    // The reported bug: at "/" with no namespace the gear was hidden purely
    // because the path equals "/", so the Welcome screen had no way into
    // settings at all.
    renderAt('/')
    expect(gear()).toBeInTheDocument()
  })

  it('navigates to /config and stays visible when no namespace is open', () => {
    renderAt('/welcome')
    fireEvent.click(gear())
    expect(screen.getByTestId('loc')).toHaveTextContent('/config')
    // The exact user-visible symptom was the gear vanishing after the click
    // (redirect back to "/", where the gear was hidden).
    expect(gear()).toBeInTheDocument()
  })

  it('stays reachable while a namespace IS open (workspace settings entry point)', () => {
    // With a namespace open the dashboard shows the namespace-scoped gear next
    // to the NS identity; the global settings gear must still be offered on the
    // right, otherwise workspace/registry settings are unreachable without
    // deactivating the namespace first.
    store.state.namespace = NS
    renderAt('/')
    expect(gear()).toBeInTheDocument()
    expect(screen.getByTitle('Namespace config')).toBeInTheDocument()
  })
})
