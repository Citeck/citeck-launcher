import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { MemoryRouter } from 'react-router'
import { Config } from './Config'
import {
  getRegistryBindings,
  getMissingRegistryAuth,
  setRegistryBinding,
  getSecrets,
  listWorkspaces,
} from '../lib/api'
import type { SecretMetaDto, WorkspaceDto } from '../lib/types'

// Spread the real module so every transitive import resolves; override only the
// calls the settings page makes.
vi.mock('../lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/api')>()),
  getHealth: vi.fn().mockResolvedValue({ healthy: true, checks: [] }),
  getRegistryBindings: vi.fn().mockResolvedValue({}),
  getMissingRegistryAuth: vi.fn().mockResolvedValue([]),
  setRegistryBinding: vi.fn().mockResolvedValue({ success: true }),
  getSecrets: vi.fn().mockResolvedValue([]),
  listWorkspaces: vi.fn().mockResolvedValue([]),
  getMigrationStatus: vi.fn().mockResolvedValue({ encrypted: true, locked: false }),
}))
vi.mock('../lib/daemonStatus', () => ({ useIsDesktop: () => true }))
vi.mock('../lib/toast', () => ({ toast: vi.fn() }))

const HOST = 'harbor.citeck.ru'

const BOUND: SecretMetaDto = {
  id: 'reg-harbor', name: 'Harbor prod', type: 'REGISTRY_AUTH', host: HOST,
} as unknown as SecretMetaDto
const OTHER: SecretMetaDto = {
  id: 'reg-backup', name: 'Harbor backup', type: 'REGISTRY_AUTH', host: HOST,
} as unknown as SecretMetaDto

const WS: WorkspaceDto = {
  id: 'ws-1', name: 'Citeck', repoUrl: 'https://github.com/Citeck/launcher-workspace.git',
  repoBranch: 'main', repoPullPeriod: 'PT2H', authType: 'NONE', active: true, namespaces: 2,
}

beforeEach(() => {
  vi.clearAllMocks()
  // jsdom has no native <dialog> modal behaviour — reflect open/close so the
  // dialog's contents stay in the accessibility tree for role queries.
  HTMLDialogElement.prototype.showModal = vi.fn(function (this: HTMLDialogElement) { this.open = true })
  HTMLDialogElement.prototype.close = vi.fn(function (this: HTMLDialogElement) { this.open = false })
  vi.mocked(getRegistryBindings).mockResolvedValue({ [HOST]: 'reg-harbor' })
  vi.mocked(getMissingRegistryAuth).mockResolvedValue([])
  vi.mocked(getSecrets).mockResolvedValue([BOUND, OTHER])
  vi.mocked(listWorkspaces).mockResolvedValue([WS])
  vi.mocked(setRegistryBinding).mockResolvedValue({ success: true } as never)
})

function renderPage() {
  return render(<MemoryRouter><Config /></MemoryRouter>)
}

// Open the per-host credentials dialog from the bindings table.
async function openHostDialog() {
  fireEvent.click(await screen.findByRole('button', { name: 'Configure' }))
  // Opened from Settings, the dialog uses management wording — "Sign in to…" /
  // "Save & Retry" belong to the pull-failure flow, where there IS a stuck pull.
  return screen.findByText(`Credentials for ${HOST}`)
}

describe('Settings page — registry credentials', () => {
  it('lists host → secret bindings from getRegistryBindings()', async () => {
    renderPage()
    // The host and the human-readable secret name (resolved via getSecrets) —
    // there was no read-only view of bindings anywhere before this.
    expect(await screen.findByText(HOST)).toBeInTheDocument()
    expect(await screen.findByText('Harbor prod')).toBeInTheDocument()
  })

  it('lists auth-required hosts that have no binding yet', async () => {
    vi.mocked(getRegistryBindings).mockResolvedValue({})
    vi.mocked(getMissingRegistryAuth).mockResolvedValue(['nexus.example.com'])
    renderPage()
    expect(await screen.findByText('nexus.example.com')).toBeInTheDocument()
    expect(await screen.findByText('Not configured')).toBeInTheDocument()
  })

  it('rebinds a host to a different credential', async () => {
    renderPage()
    await openHostDialog()

    // The dialog preselects the persisted binding; open the picker and choose
    // the other credential, then save.
    fireEvent.click(await screen.findByRole('button', { name: /Harbor prod/ }))
    fireEvent.click(await screen.findByRole('button', { name: /Harbor backup/ }))
    fireEvent.click(screen.getByText('Save credential'))

    await waitFor(() => expect(setRegistryBinding).toHaveBeenCalledWith(HOST, 'reg-backup'))
  })

  it('unbinds a host through the dialog\'s remove action', async () => {
    renderPage()
    await openHostDialog()

    const remove = screen.getByText('Remove binding') as HTMLButtonElement
    await waitFor(() => expect(remove).not.toBeDisabled())
    fireEvent.click(remove)

    // Empty secretId is the daemon's "unbind" wire shape — this is the way back
    // from a wrong credential pick.
    await waitFor(() => expect(setRegistryBinding).toHaveBeenCalledWith(HOST, ''))
  })
})

describe('Settings page — workspace', () => {
  it('shows the active workspace git settings', async () => {
    renderPage()
    expect(await screen.findByText('Citeck')).toBeInTheDocument()
    expect(await screen.findByText(WS.repoUrl)).toBeInTheDocument()
    expect(await screen.findByText('main')).toBeInTheDocument()
  })

  it('opens the workspace form (not the raw YAML editor) from the settings page', async () => {
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Workspace settings...' }))
    // The typed form — the fields users actually hunt for. Scoped to the
    // dialog: the card behind it lists the same labels read-only.
    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.getByText('Repository URL')).toBeInTheDocument()
    expect(dialog.getByText('Pull period (ISO 8601, e.g. PT2H)')).toBeInTheDocument()
    expect(dialog.getByText('Auth type')).toBeInTheDocument()
    expect(dialog.getByDisplayValue(WS.repoUrl)).toBeInTheDocument()
  })
})
