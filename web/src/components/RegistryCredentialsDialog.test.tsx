import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { describe, it, expect, beforeEach, beforeAll, vi } from 'vitest'
import { RegistryCredentialsDialog } from './RegistryCredentialsDialog'
import { getRegistryBindings, setRegistryBinding } from '../lib/api'

vi.mock('../lib/api', () => ({
  getRegistryBindings: vi.fn().mockResolvedValue({}),
  setRegistryBinding: vi.fn().mockResolvedValue({ success: true }),
  getSecrets: vi.fn().mockResolvedValue([]),
  getMigrationStatus: vi.fn().mockResolvedValue({ encrypted: true, locked: false, hasPendingSecrets: false }),
  listWorkspaces: vi.fn().mockResolvedValue([]),
  deleteSecret: vi.fn().mockResolvedValue({ success: true }),
  createSecret: vi.fn().mockResolvedValue({ success: true }),
  updateSecret: vi.fn().mockResolvedValue({}),
  setupSecretsPassword: vi.fn().mockResolvedValue({}),
  ApiError: class ApiError extends Error { code = '' },
}))

const HOST = 'harbor.citeck.ru'

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = vi.fn()
  HTMLDialogElement.prototype.close = vi.fn()
})

beforeEach(() => {
  vi.mocked(getRegistryBindings).mockResolvedValue({})
  vi.mocked(setRegistryBinding).mockClear()
})

// jsdom never sets the <dialog> `open` attribute (showModal is stubbed), so the
// dialog's contents stay out of the accessibility tree — role queries can't see
// them. Match on the button's text instead, like RegistryAuthBanner.test.tsx.
const removeButton = () => screen.getByText('Remove binding') as HTMLButtonElement

describe('RegistryCredentialsDialog remove-binding', () => {
  it('unbinds the host with an empty secretId when a binding exists', async () => {
    vi.mocked(getRegistryBindings).mockResolvedValue({ [HOST]: 'registry-harbor-a' })
    const onSaved = vi.fn()
    render(<RegistryCredentialsDialog open host={HOST} onSaved={onSaved} onClose={() => {}} />)

    const remove = removeButton()
    await waitFor(() => expect(remove).not.toBeDisabled())
    fireEvent.click(remove)

    // The daemon treats an empty secretId as "unbind" (handleSetRegistryBinding
    // in routes_registry_bindings.go) — this is the only wire shape that removes
    // a binding, and no UI exposed it before.
    await waitFor(() => expect(setRegistryBinding).toHaveBeenCalledWith(HOST, ''))
    await waitFor(() => expect(onSaved).toHaveBeenCalled())
  })

  it('disables remove when the host has no binding', async () => {
    render(<RegistryCredentialsDialog open host={HOST} onSaved={() => {}} onClose={() => {}} />)

    await waitFor(() => expect(getRegistryBindings).toHaveBeenCalled())
    const remove = removeButton()
    expect(remove).toBeDisabled()

    fireEvent.click(remove)
    expect(setRegistryBinding).not.toHaveBeenCalled()
  })
})
