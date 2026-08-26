import { render, screen } from '@testing-library/react'
import { describe, it, expect, beforeEach } from 'vitest'
import { BundleErrorBanner } from './BundleErrorBanner'
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

describe('BundleErrorBanner', () => {
  // The daemon has recorded bundleError since the empty-bundle work, and it
  // reached both the DTO and types.ts — but nothing rendered it, so a namespace
  // running seven third-party containers and none of the product looked exactly
  // like a healthy one.
  it('names the reason the bundle produced no Citeck services', () => {
    setNamespace({
      bundleError: 'bundle "community:2026.2" resolved to no applications — this namespace would start '
        + 'only third-party infrastructure, without any Citeck services',
    })
    render(<BundleErrorBanner />)

    const alert = screen.getByRole('alert')
    expect(alert.textContent).toContain('This namespace has no Citeck services')
    // The verbatim daemon reason is the only part naming WHICH ref is at fault.
    expect(alert.textContent).toContain('community:2026.2')
    expect(alert.textContent).toContain('Check the bundle repository and version')
  })

  it('stays out of the way when the bundle resolved normally', () => {
    setNamespace({ bundleRef: 'community:2026.2' })
    render(<BundleErrorBanner />)
    expect(screen.queryByRole('alert')).toBeNull()
  })

  // The daemon re-derives the verdict on every load AND every reload, so the
  // banner has to follow the store rather than latch: a namespace whose bundle
  // recovered must lose it without a remount.
  it('disappears once the daemon clears the error', () => {
    setNamespace({ bundleError: 'boom' })
    const { rerender } = render(<BundleErrorBanner />)
    expect(screen.getByRole('alert')).toBeDefined()

    setNamespace({ bundleError: '' })
    rerender(<BundleErrorBanner />)
    expect(screen.queryByRole('alert')).toBeNull()
  })
})
