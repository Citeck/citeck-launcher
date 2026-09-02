import { render, screen, waitFor, act } from '@testing-library/react'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { NamespaceEditDialog } from './NamespaceEditDialog'
import { getNamespaceEdit, putNamespaceEdit, type NamespaceEditDto } from '../lib/api'

vi.mock('../lib/api', () => ({
  getNamespaceEdit: vi.fn(),
  putNamespaceEdit: vi.fn().mockResolvedValue({}),
  createNamespace: vi.fn().mockResolvedValue({}),
  getBundles: vi.fn().mockResolvedValue([{ repo: 'community', versions: ['2026.2'] }]),
  getNamespaceCreateDefaults: vi.fn().mockResolvedValue({
    name: 'Citeck #1', bundleRepo: 'community', bundleKey: '2026.2', authType: 'KEYCLOAK',
  }),
  getWorkspaceSnapshots: vi.fn().mockResolvedValue([]),
  pullBundleRepo: vi.fn().mockResolvedValue({}),
}))
vi.mock('../lib/toast', () => ({ toast: vi.fn() }))
vi.mock('../lib/desktop', () => ({ primeDesktopModeCache: () => Promise.resolve(true) }))

function edited(patch: Partial<NamespaceEditDto>): NamespaceEditDto {
  return {
    name: 'Stand', bundleRepo: 'community', bundleKey: '2026.2', authType: 'KEYCLOAK',
    host: '', port: 80, tlsEnabled: false, ...patch,
  }
}

async function open(dto: NamespaceEditDto) {
  vi.mocked(getNamespaceEdit).mockResolvedValue(dto)
  await act(async () => {
    render(<NamespaceEditDialog open mode="edit" nsId="ns1" onClose={() => {}} />)
  })
  await waitFor(() => expect(screen.getByDisplayValue('Stand')).toBeDefined())
}

beforeEach(() => {
  // jsdom doesn't implement <dialog> showModal/close — stub them.
  HTMLDialogElement.prototype.showModal = vi.fn(function (this: HTMLDialogElement) { this.open = true })
  HTMLDialogElement.prototype.close = vi.fn(function (this: HTMLDialogElement) { this.open = false })
  vi.mocked(putNamespaceEdit).mockClear()
})

describe('NamespaceEditDialog Advanced tab', () => {
  // Only ecos-process ever used mongo and newer namespaces are created without
  // it, so the toggle is a real choice on an OLD namespace and a switch with
  // nothing behind it on a new one.
  it('offers the MongoDB toggle on a namespace created before it was dropped', async () => {
    await open(edited({ configVersion: 1, mongoEnabled: true }))

    screen.getByRole('tab', { name: 'Advanced' }).click()
    const box = await screen.findByRole('checkbox', { name: /MongoDB/ })
    expect((box as HTMLInputElement).checked).toBe(true)
  })

  it('hides the toggle — and the whole tab strip — on a namespace created without mongo', async () => {
    await open(edited({ configVersion: 2, mongoEnabled: false }))

    expect(screen.queryByRole('tab')).toBeNull()
    expect(screen.queryByRole('checkbox', { name: /MongoDB/ })).toBeNull()
  })

  // The daemon's contract is "absent field = leave unchanged". Saving a form
  // that never asked the question must not answer it.
  it('sends the choice only when the toggle was offered', async () => {
    await open(edited({ configVersion: 1, mongoEnabled: true }))
    screen.getByRole('tab', { name: 'Advanced' }).click()
    const box = await screen.findByRole('checkbox', { name: /MongoDB/ })
    await act(async () => { box.click() })
    await act(async () => { screen.getByRole('button', { name: /Save/i }).click() })

    await waitFor(() => expect(putNamespaceEdit).toHaveBeenCalled())
    expect(vi.mocked(putNamespaceEdit).mock.calls[0][1].mongoEnabled).toBe(false)

    vi.mocked(putNamespaceEdit).mockClear()
    await open(edited({ configVersion: 2, mongoEnabled: false }))
    await act(async () => { screen.getAllByRole('button', { name: /Save/i })[1].click() })
    await waitFor(() => expect(putNamespaceEdit).toHaveBeenCalled())
    expect(vi.mocked(putNamespaceEdit).mock.calls[0][1].mongoEnabled).toBeUndefined()
  })
})
