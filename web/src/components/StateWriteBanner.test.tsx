import { render, screen } from '@testing-library/react'
import { describe, it, expect, beforeEach } from 'vitest'
import { StateWriteBanner } from './StateWriteBanner'
import { useDashboardStore } from '../lib/store'
import type { NamespaceDto } from '../lib/types'

function setNamespace(patch: Partial<NamespaceDto> | null) {
  useDashboardStore.setState({
    namespace: patch === null
      ? null
      : { id: 'n', name: 'n', status: 'RUNNING', bundleRef: '', apps: [], ...patch } as NamespaceDto,
  })
}

beforeEach(() => { setNamespace(null) })

describe('StateWriteBanner', () => {
  // Detaching an app, editing an app's config or editing a mounted file all
  // answer success on a refused state write, because the action itself
  // succeeded — the container really stopped, the patch really applied. The
  // record of it did not, and until this banner the only trace anywhere in the
  // UI was nothing at all: the operator found out at the next daemon start,
  // where the app was un-detached again and the edit was gone.
  it('names why the namespace state is not reaching the store', () => {
    setNamespace({ stateWriteError: 'persist namespace state: disk quota exceeded' })
    render(<StateWriteBanner />)

    const alert = screen.getByRole('alert')
    expect(alert.textContent).toContain('Changes are not being saved')
    // The daemon's own reason is the only actionable part.
    expect(alert.textContent).toContain('disk quota exceeded')
    // And what it costs, which is the whole point of showing it.
    expect(alert.textContent).toContain('restart')
  })

  it('stays out of the way while writes are landing', () => {
    setNamespace({})
    render(<StateWriteBanner />)
    expect(screen.queryByRole('alert')).toBeNull()
  })

  // The condition is derived from the daemon's live failure streak, so the
  // first write that lands clears it with no action from anyone. The banner has
  // to follow the store rather than latch — and, like BundleErrorBanner, it is
  // deliberately not dismissible: it describes something that is true right
  // now, and hiding it re-creates exactly the invisibility it exists to remove.
  it('disappears by itself once a write lands', () => {
    setNamespace({ stateWriteError: 'disk quota exceeded' })
    const { rerender } = render(<StateWriteBanner />)
    expect(screen.getByRole('alert')).toBeDefined()
    expect(screen.queryByRole('button')).toBeNull()

    setNamespace({ stateWriteError: '' })
    rerender(<StateWriteBanner />)
    expect(screen.queryByRole('alert')).toBeNull()
  })
})
