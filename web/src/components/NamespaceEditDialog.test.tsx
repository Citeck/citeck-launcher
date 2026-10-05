import { render, screen, waitFor, act } from '@testing-library/react'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { NamespaceEditDialog } from './NamespaceEditDialog'
import {
  createNamespace,
  getContentStorageOptions,
  getNamespaceCreateDefaults,
  getNamespaceEdit,
  putNamespaceEdit,
  type NamespaceEditDto,
} from '../lib/api'
import type { ContentStorageOptionDto } from '../lib/types'

vi.mock('../lib/api', () => ({
  getNamespaceEdit: vi.fn(),
  putNamespaceEdit: vi.fn().mockResolvedValue({}),
  createNamespace: vi.fn().mockResolvedValue({}),
  // A fresh list per call, like a real JSON response: a refresh that hands
  // back the same array object would not re-render anything.
  getBundles: vi.fn(async () => [
    { repo: 'community', versions: ['2026.2'] },
    { repo: 'enterprise', versions: ['2026.3', '2026.2'] },
  ]),
  // Default: a Community bundle, which has no ecos-content and therefore no
  // content storage question at all.
  getContentStorageOptions: vi.fn().mockResolvedValue({ s3: { available: false, reason: 'NO_CONTENT_APP' } }),
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
  vi.mocked(createNamespace).mockClear()
  vi.mocked(getContentStorageOptions).mockReset()
  vi.mocked(getContentStorageOptions).mockResolvedValue({ s3: { available: false, reason: 'NO_CONTENT_APP' } })
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

// The options endpoint answers per bundle; these tests answer by "repo:key",
// falling back to the repo alone.
function optionsByRepo(byBundle: Record<string, ContentStorageOptionDto>) {
  vi.mocked(getContentStorageOptions).mockImplementation(async (repo: string, key: string) => ({
    s3: byBundle[`${repo}:${key}`] ?? byBundle[repo] ?? { available: false, reason: 'NO_CONTENT_APP' },
  }))
}

const AVAILABLE: ContentStorageOptionDto = { available: true, providerApp: 'seaweedfs' }

async function openCreate(defaults: { bundleRepo: string; bundleKey: string }) {
  vi.mocked(getNamespaceCreateDefaults).mockResolvedValue({
    name: 'Citeck #1', authType: 'KEYCLOAK', ...defaults,
  })
  await act(async () => {
    render(<NamespaceEditDialog open mode="create" onClose={() => {}} />)
  })
  await waitFor(() => expect(screen.getByDisplayValue('Citeck #1')).toBeDefined())
}

async function submit(label: RegExp) {
  await act(async () => { screen.getAllByRole('button', { name: label }).at(-1)!.click() })
}

describe('NamespaceEditDialog content storage', () => {
  // Community bundles have no ecos-content: the form must stay exactly what it
  // was, and the payload must not carry the field.
  it('asks nothing on a bundle without ecos-content, and sends nothing', async () => {
    await openCreate({ bundleRepo: 'community', bundleKey: '2026.2' })
    await waitFor(() => expect(getContentStorageOptions).toHaveBeenCalledWith('community', '2026.2', undefined, expect.anything()))

    expect(screen.queryByText('Content storage')).toBeNull()
    expect(screen.queryByRole('radio')).toBeNull()

    await submit(/^Submit$/)
    await waitFor(() => expect(createNamespace).toHaveBeenCalled())
    expect(vi.mocked(createNamespace).mock.calls[0][0].contentStorage).toBeUndefined()
  })

  it('offers S3 on an Enterprise bundle, defaults to the database, and sends S3 only when chosen', async () => {
    optionsByRepo({ enterprise: AVAILABLE })
    await openCreate({ bundleRepo: 'enterprise', bundleKey: '2026.3' })

    const db = await screen.findByRole('radio', { name: /Database/ })
    const s3 = screen.getByRole('radio', { name: /S3 \(SeaweedFS\)/ })
    expect((db as HTMLInputElement).checked).toBe(true)
    expect((s3 as HTMLInputElement).disabled).toBe(false)
    // The one-way note appears only once S3 is the choice.
    expect(screen.queryByText(/The choice is final/)).toBeNull()

    await act(async () => { s3.click() })
    expect(screen.getByText(/seaweedfs will start with the namespace/)).toBeDefined()
    expect(screen.getByText(/The choice is final/)).toBeDefined()

    await submit(/^Submit$/)
    await waitFor(() => expect(createNamespace).toHaveBeenCalled())
    expect(vi.mocked(createNamespace).mock.calls[0][0].contentStorage).toBe('S3')
  })

  it('shows S3 disabled, with the reason, when the workspace declares no provider', async () => {
    optionsByRepo({ enterprise: { available: false, reason: 'NO_PROVIDER_CONFIG' } })
    await openCreate({ bundleRepo: 'enterprise', bundleKey: '2026.3' })

    const s3 = await screen.findByRole('radio', { name: /^S3/ })
    expect((s3 as HTMLInputElement).disabled).toBe(true)
    expect(screen.getByText(/declares no S3 provider/)).toBeDefined()
  })

  // S3 is irreversible: a choice made for a bundle that can have it must not
  // survive a move to one that cannot — it reads, and saves, as the database.
  it('drops an S3 choice when the bundle changes to one that cannot have it', async () => {
    optionsByRepo({ enterprise: AVAILABLE })
    await openCreate({ bundleRepo: 'enterprise', bundleKey: '2026.3' })
    await act(async () => { (await screen.findByRole('radio', { name: /^S3/ })).click() })

    optionsByRepo({ enterprise: { available: false, reason: 'NO_PROVIDER_IMAGE', providerApp: 'seaweedfs' } })
    // The refresh button re-reads the bundle list, which re-asks the daemon.
    await act(async () => { screen.getByRole('button', { name: 'Refresh bundles (git pull)' }).click() })
    await waitFor(() => expect(screen.getByText(/names an image for seaweedfs/)).toBeDefined())
    expect((screen.getByRole('radio', { name: /Database/ }) as HTMLInputElement).checked).toBe(true)

    await submit(/^Submit$/)
    await waitFor(() => expect(createNamespace).toHaveBeenCalled())
    expect(vi.mocked(createNamespace).mock.calls[0][0].contentStorage).toBeUndefined()
  })

  // An answer is about ONE bundle. Until the daemon has answered for the new
  // selection, the previous bundle's "available" must not keep S3 chosen.
  it('does not carry an answer over to a bundle the daemon has not answered for yet', async () => {
    vi.mocked(getContentStorageOptions).mockImplementation((_repo: string, key: string) =>
      key === '2026.3' ? Promise.resolve({ s3: AVAILABLE }) : new Promise(() => {}))
    await openCreate({ bundleRepo: 'enterprise', bundleKey: '2026.3' })
    await act(async () => { (await screen.findByRole('radio', { name: /^S3/ })).click() })

    await act(async () => { screen.getByRole('button', { name: /2026\.3/ }).click() })
    await act(async () => { screen.getByRole('option', { name: '2026.2' }).click() })

    expect(screen.queryByRole('radio', { name: /^S3/ })).toBeNull()
    await submit(/^Submit$/)
    await waitFor(() => expect(createNamespace).toHaveBeenCalled())
    expect(vi.mocked(createNamespace).mock.calls[0][0].contentStorage).toBeUndefined()
  })

  it('lets a DB namespace switch to S3, and sends exactly that switch', async () => {
    optionsByRepo({ enterprise: AVAILABLE })
    await open(edited({ bundleRepo: 'enterprise', bundleKey: '2026.3', contentStorage: 'DB' }))

    const s3 = await screen.findByRole('radio', { name: /^S3/ })
    await act(async () => { s3.click() })
    expect(screen.getByText(/The switch is one-way/)).toBeDefined()

    await submit(/^Save$/)
    await waitFor(() => expect(putNamespaceEdit).toHaveBeenCalled())
    expect(vi.mocked(putNamespaceEdit).mock.calls[0][1].contentStorage).toBe('S3')
  })

  it('leaves a DB namespace untouched when the database stays chosen', async () => {
    optionsByRepo({ enterprise: AVAILABLE })
    await open(edited({ bundleRepo: 'enterprise', bundleKey: '2026.3', contentStorage: 'DB' }))
    await screen.findByRole('radio', { name: /^S3/ })

    await submit(/^Save$/)
    await waitFor(() => expect(putNamespaceEdit).toHaveBeenCalled())
    expect(vi.mocked(putNamespaceEdit).mock.calls[0][1].contentStorage).toBeUndefined()
  })

  // S3 → DB is refused by the daemon whatever is sent; the form shows S3 as a
  // fact, offers no way back, and sends nothing (absent = unchanged).
  it('shows an S3 namespace read-only, even on a bundle the daemon says nothing about', async () => {
    vi.mocked(getContentStorageOptions).mockRejectedValue(new Error('offline'))
    await open(edited({ bundleRepo: 'enterprise', bundleKey: '2026.3', contentStorage: 'S3' }))

    expect(await screen.findByText('Content storage')).toBeDefined()
    expect(screen.queryByRole('radio')).toBeNull()
    expect(screen.getByText(/cannot be switched back to the database/)).toBeDefined()

    await submit(/^Save$/)
    await waitFor(() => expect(putNamespaceEdit).toHaveBeenCalled())
    expect(vi.mocked(putNamespaceEdit).mock.calls[0][1].contentStorage).toBeUndefined()
  })

  // The daemon re-asks availability only when the bundle CHANGES under an S3
  // namespace (an edit keeping it passes — or the namespace could not even be
  // renamed once the workspace changed). The warning follows the same rule.
  it('warns that moving an S3 namespace to a bundle without the provider will be refused', async () => {
    // Unavailable for BOTH versions: what differs is only whether the bundle
    // moved.
    optionsByRepo({
      'enterprise:2026.3': { available: false, reason: 'NO_PROVIDER_IMAGE', providerApp: 'seaweedfs' },
      'enterprise:2026.2': { available: false, reason: 'NO_PROVIDER_IMAGE', providerApp: 'seaweedfs' },
    })
    await open(edited({ bundleRepo: 'enterprise', bundleKey: '2026.3', contentStorage: 'S3' }))
    // The provider's name comes from the answer — so it is in.
    await screen.findByText('S3 (SeaweedFS)')
    expect(screen.queryByText(/Saving will be refused/)).toBeNull()

    await act(async () => { screen.getByRole('button', { name: /2026\.3/ }).click() })
    await act(async () => { screen.getByRole('option', { name: '2026.2' }).click() })

    const warning = await screen.findByText(/Saving will be refused/)
    expect(warning.textContent).toContain('names an image for seaweedfs')
  })
})
