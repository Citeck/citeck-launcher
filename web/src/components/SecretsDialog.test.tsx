import { render, screen, waitFor } from '@testing-library/react'
import { describe, it, expect, beforeEach, beforeAll, vi } from 'vitest'
import { SecretsDialog } from './SecretsDialog'
import { getSecrets } from '../lib/api'
import type { SecretMetaDto } from '../lib/types'

vi.mock('../lib/api', () => ({
  getSecrets: vi.fn().mockResolvedValue([]),
  createSecret: vi.fn().mockResolvedValue({ success: true }),
  deleteSecret: vi.fn().mockResolvedValue({ success: true }),
  testSecret: vi.fn().mockResolvedValue({ success: true }),
  setupSecretsPassword: vi.fn().mockResolvedValue({}),
  listWorkspaces: vi.fn().mockResolvedValue([]),
  updateSecret: vi.fn().mockResolvedValue({}),
  getMigrationStatus: vi.fn().mockResolvedValue({ encrypted: true, locked: false, hasPendingSecrets: false }),
  ApiError: class ApiError extends Error { code = '' },
}))

const SECRETS: SecretMetaDto[] = [
  { id: 'registry-harbor', name: 'Harbor', type: 'REGISTRY_AUTH', scope: '', host: 'harbor.citeck.ru', createdAt: '' },
  { id: 'git-token-gitlab', name: 'GitLab', type: 'GIT_TOKEN', scope: '', createdAt: '' },
]

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = vi.fn()
  HTMLDialogElement.prototype.close = vi.fn()
})

beforeEach(() => {
  vi.mocked(getSecrets).mockResolvedValue(SECRETS)
})

describe('SecretsDialog host column', () => {
  it('shows each secret\'s host, and a dash for host-agnostic ones', async () => {
    render(<SecretsDialog open onClose={() => {}} />)

    // The daemon populates SecretMetaDto.host, but the table dropped it — a
    // credential bound to the wrong registry was invisible here.
    await waitFor(() => expect(screen.getByText('Harbor')).toBeInTheDocument())
    expect(screen.getByText('Host')).toBeInTheDocument()
    expect(screen.getByText('harbor.citeck.ru')).toBeInTheDocument()
    // Secrets created through this generic dialog never set a host — render the
    // gap rather than inventing a value.
    expect(screen.getByText('—')).toBeInTheDocument()
  })
})
