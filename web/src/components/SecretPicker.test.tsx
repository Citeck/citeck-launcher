import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, beforeEach, beforeAll, vi } from 'vitest'
import { SecretPicker } from './SecretPicker'
import { getSecrets } from '../lib/api'
import type { SecretMetaDto } from '../lib/types'

// The picker fetches secrets/workspaces on mount; stub the module so tests stay
// offline and deterministic (same idiom as RegistryAuthBanner.test.tsx).
vi.mock('../lib/api', () => ({
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
const OTHER = 'other.example.com'

// Deliberately interleaved: the foreign-host secret sits FIRST, so a secret's
// index in the full list never equals its index in the host-filtered list.
// That gap is exactly what the activeIdx bug fell into.
const SECRETS: SecretMetaDto[] = [
  { id: 'registry-other', name: 'Other Registry', type: 'REGISTRY_AUTH', scope: '', host: OTHER, createdAt: '' },
  { id: 'registry-harbor-a', name: 'Harbor A', type: 'REGISTRY_AUTH', scope: '', host: HOST, createdAt: '' },
  { id: 'registry-harbor-b', name: 'Harbor B', type: 'REGISTRY_AUTH', scope: '', host: HOST, createdAt: '' },
  { id: 'registry-harbor-c', name: 'Harbor C', type: 'REGISTRY_AUTH', scope: '', host: HOST, createdAt: '' },
]

beforeAll(() => {
  // jsdom doesn't implement <dialog> showModal/close — stub them.
  HTMLDialogElement.prototype.showModal = vi.fn()
  HTMLDialogElement.prototype.close = vi.fn()
})

beforeEach(() => {
  vi.mocked(getSecrets).mockResolvedValue(SECRETS)
})

describe('SecretPicker host filtering / active row', () => {
  it('commits the CURRENTLY SELECTED secret on click-open + Enter (not the row at its unfiltered index)', async () => {
    const onChange = vi.fn()
    render(
      <SecretPicker secretType="REGISTRY_AUTH" host={HOST} value="registry-harbor-b" onChange={onChange} />,
    )

    // Trigger shows the selection once the list has loaded.
    const trigger = await screen.findByRole('button', { name: /Harbor B/ })

    // Open by MOUSE, then commit with Enter. The mouse path used to seed
    // activeIdx from the FULL list while Enter reads the host-FILTERED one:
    // 'registry-harbor-b' is index 2 unfiltered but index 1 among HOST rows,
    // so Enter committed 'Harbor C'.
    fireEvent.click(trigger)
    fireEvent.keyDown(trigger, { key: 'Enter' })

    expect(onChange).toHaveBeenCalledWith('registry-harbor-b')
  })

  it('surfaces a value bound to ANOTHER host as an explicit mismatch instead of a valid-looking label', async () => {
    const onChange = vi.fn()
    render(
      <SecretPicker secretType="REGISTRY_AUTH" host={HOST} value="registry-other" onChange={onChange} />,
    )

    // The trigger must name the secret's ACTUAL host — rendering it as a plain
    // label made a cross-host binding indistinguishable from a correct one.
    const warning = await screen.findByLabelText(new RegExp(`belongs to ${OTHER}`, 'i'))
    expect(warning).toHaveTextContent('Other Registry')
    expect(warning).toHaveTextContent(OTHER)

    // Sanity: the popup is still closed, so the assertions below are about the
    // opened list and not about rows that were already on screen.
    expect(screen.queryByRole('option')).not.toBeInTheDocument()
    fireEvent.click(warning.closest('button') as HTMLButtonElement)

    // Once open, the mismatched secret is LISTED (flagged with its host), so
    // the user can see and replace what is actually bound rather than hunting
    // for a row the host filter had removed.

    const row = await screen.findByRole('option', { selected: true })
    expect(row).toHaveTextContent('Other Registry')
    expect(row).toHaveTextContent(OTHER)
  })

  it('re-derives the active row when "show all hosts" widens the list', async () => {
    const onChange = vi.fn()
    render(
      <SecretPicker secretType="REGISTRY_AUTH" host={HOST} value="registry-harbor-b" onChange={onChange} />,
    )
    const trigger = await screen.findByRole('button', { name: /Harbor B/ })

    // Keyboard-open seeds activeIdx=1 (Harbor B within the HOST rows).
    fireEvent.keyDown(trigger, { key: 'ArrowDown' })
    // Widening to all hosts makes index 1 mean 'Harbor A' — activeIdx must be
    // re-derived against the new list, not carried over.
    fireEvent.click(await screen.findByText(/Show secrets for other hosts/))
    fireEvent.keyDown(trigger, { key: 'Enter' })

    expect(onChange).toHaveBeenCalledWith('registry-harbor-b')
  })

  it('re-derives the active row when the host prop changes under an open popup', async () => {
    const onChange = vi.fn()
    const { rerender } = render(
      <SecretPicker secretType="REGISTRY_AUTH" host={HOST} value="registry-harbor-b" onChange={onChange} />,
    )
    const trigger = await screen.findByRole('button', { name: /Harbor B/ })

    fireEvent.keyDown(trigger, { key: 'ArrowDown' })
    // Host switches while the popup is open (the dialog stays mounted across
    // hosts in the registry pre-flight). A stale activeIdx pointed past the new
    // list and Enter fell through to "Add new…".
    rerender(
      <SecretPicker secretType="REGISTRY_AUTH" host={OTHER} value="registry-harbor-b" onChange={onChange} />,
    )
    fireEvent.keyDown(trigger, { key: 'Enter' })

    expect(onChange).toHaveBeenCalledWith('registry-harbor-b')
  })
})
