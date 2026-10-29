import { render, screen, waitFor, act } from '@testing-library/react'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { NamespaceEditDialog } from './NamespaceEditDialog'
import {
  createNamespace,
  getContentStorageOptions,
  getExperimentalFeatures,
  getNamespaceCreateDefaults,
  getNamespaceEdit,
  putNamespaceEdit,
  type NamespaceEditDto,
} from '../lib/api'
import type { ContentStorageOptionDto, ExperimentalFeatureDto } from '../lib/types'

vi.mock('../lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/api')>()),
  getNamespaceEdit: vi.fn(),
  putNamespaceEdit: vi.fn().mockResolvedValue({}),
  createNamespace: vi.fn().mockResolvedValue({}),
  // A fresh list per call, like a real JSON response: a refresh that hands
  // back the same array object would not re-render anything.
  getBundles: vi.fn(async () => [
    { repo: 'community', versions: ['2026.2'] },
    { repo: 'enterprise', versions: ['2026.3', '2026.2'] },
  ]),
  // Default: a Community bundle without ecos-content; S3 is shown disabled.
  getContentStorageOptions: vi.fn().mockResolvedValue({ s3: { available: false, reason: 'NO_CONTENT_APP' } }),
  // Default: a bundle with no beta modules; the section stays hidden.
  getExperimentalFeatures: vi.fn().mockResolvedValue({ features: [] }),
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
  vi.mocked(getExperimentalFeatures).mockReset()
  vi.mocked(getExperimentalFeatures).mockResolvedValue({ features: [] })
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

const AVAILABLE: ContentStorageOptionDto = { available: true, providerApp: 'seaweedfs', providerTitle: 'SeaweedFS' }

async function openCreate(defaults: { bundleRepo: string; bundleKey: string }) {
  vi.mocked(getNamespaceCreateDefaults).mockResolvedValue({
    name: 'Citeck #1', authType: 'KEYCLOAK', ...defaults,
  })
  await act(async () => {
    render(<NamespaceEditDialog open mode="create" onClose={() => {}} />)
  })
  await waitFor(() => expect(screen.getByDisplayValue('Citeck #1')).toBeDefined())
}

async function waitForStorage() {
  await screen.findByText('Content storage')
}

async function submit(label: RegExp) {
  await act(async () => { screen.getAllByRole('button', { name: label }).at(-1)!.click() })
}

const SCANNER: ExperimentalFeatureDto = {
  id: 'documentScanner', title: 'Document scanning', description: 'New OCR service.',
  apps: ['ecos-scanner', 'scanner-postgres'],
}
const BACKUP: ExperimentalFeatureDto = { id: 'backupEngineV2', title: 'backupEngineV2', apps: ['backup-engine'] }

describe('NamespaceEditDialog experimental features', () => {
  it('shows no section when the bundle offers nothing, and sends nothing', async () => {
    await openCreate({ bundleRepo: 'community', bundleKey: '2026.2' })
    await waitFor(() => expect(getExperimentalFeatures).toHaveBeenCalledWith('community', '2026.2', undefined, expect.anything()))
    expect(screen.queryByRole('tab', { name: 'Experimental' })).toBeNull()
    expect(screen.queryByText('Experimental features')).toBeNull()

    await submit(/^Submit$/)
    await waitFor(() => expect(createNamespace).toHaveBeenCalled())
    expect(vi.mocked(createNamespace).mock.calls[0][0].experimentalFeatures).toBeUndefined()
  })

  // The beta modules live on a tab of their own. Every row is marked BETA,
  // and what is sent is exactly what was switched on.
  it('lists the offered features on their own tab with a BETA badge and sends the ones switched on', async () => {
    vi.mocked(getExperimentalFeatures).mockResolvedValue({ features: [BACKUP, SCANNER] })
    await openCreate({ bundleRepo: 'enterprise', bundleKey: '2026.3' })
    expect(screen.queryByText('Experimental features')).toBeNull()
    await act(async () => { (await screen.findByRole('tab', { name: 'Experimental' })).click() })
    await screen.findByText('Experimental features')

    expect(screen.getAllByText('BETA').length).toBeGreaterThanOrEqual(3)
    expect(screen.getByText('New OCR service.')).toBeDefined()
    expect(screen.getByText(/ecos-scanner, scanner-postgres/)).toBeDefined()
    expect(screen.getByText(/may change or be removed/)).toBeDefined()
    const scanner = screen.getByRole('checkbox', { name: /Document scanning/ })
    expect((scanner as HTMLInputElement).checked).toBe(false)
    await act(async () => { scanner.click() })

    await submit(/^Submit$/)
    await waitFor(() => expect(createNamespace).toHaveBeenCalled())
    expect(vi.mocked(createNamespace).mock.calls[0][0].experimentalFeatures).toEqual(['documentScanner'])
  })

  // The daemon's contract is "absent = leave unchanged": a form nobody touched
  // must not answer the question.
  it('seeds the switches from the edit GET and sends nothing when none was touched', async () => {
    vi.mocked(getExperimentalFeatures).mockResolvedValue({ features: [SCANNER, BACKUP] })
    await open(edited({ experimentalFeatures: [{ ...SCANNER, enabled: true }, BACKUP] }))
    await act(async () => { (await screen.findByRole('tab', { name: 'Experimental' })).click() })

    const scanner = await screen.findByRole('checkbox', { name: /Document scanning/ })
    expect((scanner as HTMLInputElement).checked).toBe(true)
    expect((screen.getByRole('checkbox', { name: /backupEngineV2/ }) as HTMLInputElement).checked).toBe(false)

    await submit(/^Save$/)
    await waitFor(() => expect(putNamespaceEdit).toHaveBeenCalled())
    expect(vi.mocked(putNamespaceEdit).mock.calls[0][1].enabledExperimentalFeatures).toBeUndefined()
  })

  it('warns when a running module is switched off, and sends the remaining set', async () => {
    vi.mocked(getExperimentalFeatures).mockResolvedValue({ features: [SCANNER, BACKUP] })
    await open(edited({ experimentalFeatures: [{ ...SCANNER, enabled: true }, BACKUP] }))
    await act(async () => { (await screen.findByRole('tab', { name: 'Experimental' })).click() })

    const scanner = await screen.findByRole('checkbox', { name: /Document scanning/ })
    expect(screen.queryByText(/removes its containers/)).toBeNull()
    await act(async () => { scanner.click() })
    expect(screen.getByText(/removes its containers/)).toBeDefined()

    await submit(/^Save$/)
    await waitFor(() => expect(putNamespaceEdit).toHaveBeenCalled())
    expect(vi.mocked(putNamespaceEdit).mock.calls[0][1].enabledExperimentalFeatures).toEqual([])
  })

  it.each([false, true])('restores inert selections on rollback and preserves them when another choice changes (%s)', async (changeBackup) => {
    vi.mocked(getExperimentalFeatures).mockImplementation(async (_repo, key) => ({
      features: key === '2026.3' ? [] : [SCANNER, BACKUP],
    }))
    await open(edited({
      bundleRepo: 'enterprise', bundleKey: '2026.3', experimentalFeatures: [],
      storedExperimentalFeatures: ['documentScanner', 'retiredFeature'],
    }))
    await act(async () => { screen.getByRole('button', { name: /2026\.3/ }).click() })
    await act(async () => { screen.getByRole('option', { name: '2026.2' }).click() })
    await act(async () => { (await screen.findByRole('tab', { name: 'Experimental' })).click() })
    expect((screen.getByRole('checkbox', { name: /Document scanning/ }) as HTMLInputElement).checked).toBe(true)
    if (changeBackup) {
      await act(async () => { screen.getByRole('checkbox', { name: /backupEngineV2/ }).click() })
    }
    await submit(/^Save$/)
    await waitFor(() => expect(putNamespaceEdit).toHaveBeenCalled())
    expect(vi.mocked(putNamespaceEdit).mock.calls[0][1].enabledExperimentalFeatures)
      .toEqual(changeBackup ? ['documentScanner', 'backupEngineV2'] : undefined)
  })

  it('does not replace feature choices on a bundle-only edit with an older daemon', async () => {
    vi.mocked(getExperimentalFeatures).mockResolvedValue({ features: [SCANNER] })
    await open(edited({ bundleRepo: 'enterprise', bundleKey: '2026.3' }))
    await act(async () => { screen.getByRole('button', { name: /2026\.3/ }).click() })
    await act(async () => { screen.getByRole('option', { name: '2026.2' }).click() })
    await screen.findByRole('tab', { name: 'Experimental' })
    await submit(/^Save$/)
    await waitFor(() => expect(putNamespaceEdit).toHaveBeenCalled())
    expect(vi.mocked(putNamespaceEdit).mock.calls[0][1].enabledExperimentalFeatures).toBeUndefined()
  })

  // Nothing is offered for a bundle that is not on disk: the section would
  // only promise what saving refuses.
  it('hides the section while the bundle is not resolved', async () => {
    vi.mocked(getExperimentalFeatures).mockResolvedValue({ features: [], reason: 'BUNDLE_UNRESOLVED' })
    await openCreate({ bundleRepo: 'enterprise', bundleKey: '2026.3' })
    await waitFor(() => expect(getExperimentalFeatures).toHaveBeenCalled())
    expect(screen.queryByRole('tab', { name: 'Experimental' })).toBeNull()
    expect(screen.queryByText('Experimental features')).toBeNull()
  })
})

describe('NamespaceEditDialog content storage', () => {
  // Community explains the Enterprise-only S3 feature without allowing it.
  // The create request still explicitly chooses DB, never a template default.
  it('shows disabled Enterprise S3 on a bundle without ecos-content, and creates on the database', async () => {
    await openCreate({ bundleRepo: 'community', bundleKey: '2026.2' })
    await waitFor(() => expect(getContentStorageOptions).toHaveBeenCalledWith('community', '2026.2', undefined, expect.anything()))

    const s3 = await screen.findByRole('radio', { name: /^S3/ })
    expect((s3 as HTMLInputElement).disabled).toBe(true)
    expect(screen.getByText(/S3 content storage is available only in Citeck Enterprise/)).toBeDefined()
    expect((screen.getByRole('radio', { name: /Database/ }) as HTMLInputElement).checked).toBe(true)
    await act(async () => { s3.click() })

    await submit(/^Submit$/)
    await waitFor(() => expect(createNamespace).toHaveBeenCalled())
    expect(vi.mocked(createNamespace).mock.calls[0][0].contentStorage).toBe('DB')
  })

  it('keeps a Community DB namespace unchanged while explaining disabled S3', async () => {
    await open(edited({ configVersion: 2, contentStorage: 'DB' }))
    const s3 = await screen.findByRole('radio', { name: /^S3/ })
    expect((s3 as HTMLInputElement).disabled).toBe(true)
    expect(screen.getByText(/S3 content storage is available only in Citeck Enterprise/)).toBeDefined()
    await submit(/^Save$/)
    await waitFor(() => expect(putNamespaceEdit).toHaveBeenCalled())
    expect(vi.mocked(putNamespaceEdit).mock.calls[0][1].contentStorage).toBeUndefined()
  })

  it('resets an Enterprise S3 choice to DB when switching to Community', async () => {
    optionsByRepo({ enterprise: AVAILABLE })
    await openCreate({ bundleRepo: 'enterprise', bundleKey: '2026.3' })
    await act(async () => { (await screen.findByRole('radio', { name: /^S3/ })).click() })
    await act(async () => { screen.getByRole('button', { name: 'enterprise' }).click() })
    await act(async () => { screen.getByRole('option', { name: 'community' }).click() })
    await screen.findByText(/S3 content storage is available only in Citeck Enterprise/)
    expect((screen.getByRole('radio', { name: /^S3/ }) as HTMLInputElement).disabled).toBe(true)
    expect((screen.getByRole('radio', { name: /Database/ }) as HTMLInputElement).checked).toBe(true)
    await submit(/^Submit$/)
    await waitFor(() => expect(createNamespace).toHaveBeenCalled())
    expect(vi.mocked(createNamespace).mock.calls[0][0].contentStorage).toBe('DB')
  })

  // What the form shows selected is exactly what is sent.
  it('sends the database when the database is what the form shows', async () => {
    optionsByRepo({ enterprise: AVAILABLE })
    await openCreate({ bundleRepo: 'enterprise', bundleKey: '2026.3' })
    const db = await screen.findByRole('radio', { name: /Database/ })
    expect((db as HTMLInputElement).checked).toBe(true)
    expect(screen.queryByRole('tab', { name: 'Advanced' })).toBeNull()

    await submit(/^Submit$/)
    await waitFor(() => expect(createNamespace).toHaveBeenCalled())
    expect(vi.mocked(createNamespace).mock.calls[0][0].contentStorage).toBe('DB')
  })

  it('offers S3 on an Enterprise bundle, defaults to the database, and sends S3 only when chosen', async () => {
    optionsByRepo({ enterprise: AVAILABLE })
    await openCreate({ bundleRepo: 'enterprise', bundleKey: '2026.3' })
    await waitForStorage()

    const db = await screen.findByRole('radio', { name: /Database/ })
    const s3 = screen.getByRole('radio', { name: /S3 \(SeaweedFS\)/ })
    expect((db as HTMLInputElement).checked).toBe(true)
    expect((s3 as HTMLInputElement).disabled).toBe(false)
    // The one-way note appears only once S3 is the choice.
    expect(screen.queryByText(/The choice is final/)).toBeNull()

    await act(async () => { s3.click() })
    expect(screen.getByText(/seaweedfs will start with the namespace/)).toBeDefined()
    expect(screen.getByText(/The choice is final/)).toBeDefined()
    expect((s3 as HTMLInputElement).checked).toBe(true)

    await submit(/^Submit$/)
    await waitFor(() => expect(createNamespace).toHaveBeenCalled())
    expect(vi.mocked(createNamespace).mock.calls[0][0].contentStorage).toBe('S3')
  })

  it('keeps storage on General and only legacy MongoDB on Advanced, preserving the S3 choice', async () => {
    optionsByRepo({ enterprise: AVAILABLE })
    await open(edited({ bundleRepo: 'enterprise', bundleKey: '2026.3', configVersion: 1, mongoEnabled: true, contentStorage: 'DB' }))
    const s3 = await screen.findByRole('radio', { name: /^S3/ })
    expect(screen.queryByRole('checkbox', { name: /MongoDB/ })).toBeNull()
    await act(async () => { s3.click() })
    await act(async () => { screen.getByRole('tab', { name: 'Advanced' }).click() })
    expect(screen.getByRole('checkbox', { name: /MongoDB/ })).toBeDefined()
    expect(screen.queryByText('Content storage')).toBeNull()
    await act(async () => { screen.getByRole('tab', { name: 'General' }).click() })
    expect((screen.getByRole('radio', { name: /^S3/ }) as HTMLInputElement).checked).toBe(true)
    await submit(/^Save$/)
    await waitFor(() => expect(putNamespaceEdit).toHaveBeenCalled())
    expect(vi.mocked(putNamespaceEdit).mock.calls[0][1].contentStorage).toBe('S3')
  })

  it('shows S3 disabled, with the reason, when the workspace declares no provider', async () => {
    optionsByRepo({ enterprise: { available: false, reason: 'NO_PROVIDER_CONFIG' } })
    await openCreate({ bundleRepo: 'enterprise', bundleKey: '2026.3' })
    await waitForStorage()

    const s3 = await screen.findByRole('radio', { name: /^S3/ })
    expect((s3 as HTMLInputElement).disabled).toBe(true)
    expect(screen.getByText(/declares no S3 provider/)).toBeDefined()
  })

  // S3 is irreversible: a choice made for a bundle that can have it must not
  // survive a move to one that cannot — it reads, and saves, as the database.
  it('drops an S3 choice when the bundle changes to one that cannot have it', async () => {
    optionsByRepo({ enterprise: AVAILABLE })
    await openCreate({ bundleRepo: 'enterprise', bundleKey: '2026.3' })
    await waitForStorage()
    await act(async () => { (await screen.findByRole('radio', { name: /^S3/ })).click() })

    optionsByRepo({ enterprise: { available: false, reason: 'NO_PROVIDER_IMAGE', providerApp: 'seaweedfs' } })
    // The refresh button re-reads the bundle list, which re-asks the daemon.
    await act(async () => { screen.getByRole('button', { name: 'Refresh bundles (git pull)' }).click() })
    await waitForStorage()
    await waitFor(() => expect(screen.getByText(/names an image for seaweedfs/)).toBeDefined())
    expect((screen.getByRole('radio', { name: /Database/ }) as HTMLInputElement).checked).toBe(true)

    await submit(/^Submit$/)
    await waitFor(() => expect(createNamespace).toHaveBeenCalled())
    expect(vi.mocked(createNamespace).mock.calls[0][0].contentStorage).toBe('DB')
  })

  // An answer is about ONE bundle. Until the daemon has answered for the new
  // selection, the previous bundle's "available" must not keep S3 chosen.
  it('does not carry an answer over to a bundle the daemon has not answered for yet', async () => {
    vi.mocked(getContentStorageOptions).mockImplementation((_repo: string, key: string) =>
      key === '2026.3' ? Promise.resolve({ s3: AVAILABLE }) : new Promise(() => {}))
    await openCreate({ bundleRepo: 'enterprise', bundleKey: '2026.3' })
    await waitForStorage()
    await act(async () => { (await screen.findByRole('radio', { name: /^S3/ })).click() })

    await act(async () => { screen.getByRole('button', { name: /2026\.3/ }).click() })
    await act(async () => { screen.getByRole('option', { name: '2026.2' }).click() })

    expect(screen.queryByRole('radio', { name: /^S3/ })).toBeNull()
    await submit(/^Submit$/)
    await waitFor(() => expect(createNamespace).toHaveBeenCalled())
    expect(vi.mocked(createNamespace).mock.calls[0][0].contentStorage).toBe('DB')
  })

  it('lets a DB namespace switch to S3, and sends exactly that switch', async () => {
    optionsByRepo({ enterprise: AVAILABLE })
    await open(edited({ bundleRepo: 'enterprise', bundleKey: '2026.3', contentStorage: 'DB' }))
    await waitForStorage()

    const s3 = await screen.findByRole('radio', { name: /^S3/ })
    await act(async () => { s3.click() })
    expect(screen.getByText(/The switch is one-way/)).toBeDefined()

    await submit(/^Save$/)
    await waitFor(() => expect(putNamespaceEdit).toHaveBeenCalled())
    expect(vi.mocked(putNamespaceEdit).mock.calls[0][1].contentStorage).toBe('S3')
  })

  // The options endpoint answers for the bundle; only the edit GET knows the
  // namespace already keeps its content in an external S3, which S3 content
  // storage cannot be combined with. Offering the switch would only lead to a
  // refusal on save.
  it('shows S3 disabled on a namespace with an external S3, and sends nothing', async () => {
    optionsByRepo({ enterprise: AVAILABLE })
    await open(edited({ bundleRepo: 'enterprise', bundleKey: '2026.3', contentStorage: 'DB', externalS3: true }))
    await waitForStorage()

    const s3 = await screen.findByRole('radio', { name: /^S3 \(SeaweedFS\)/ })
    expect((s3 as HTMLInputElement).disabled).toBe(true)
    expect(screen.getByText(/keeps its content in an external S3/)).toBeDefined()

    await submit(/^Save$/)
    await waitFor(() => expect(putNamespaceEdit).toHaveBeenCalled())
    expect(vi.mocked(putNamespaceEdit).mock.calls[0][1].contentStorage).toBeUndefined()
  })

  it('leaves a DB namespace untouched when the database stays chosen', async () => {
    optionsByRepo({ enterprise: AVAILABLE })
    await open(edited({ bundleRepo: 'enterprise', bundleKey: '2026.3', contentStorage: 'DB' }))
    await waitForStorage()
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
    await waitForStorage()

    expect(await screen.findByText('Content storage')).toBeDefined()
    expect(screen.queryByRole('radio')).toBeNull()
    expect(screen.getByText(/cannot be switched back to storing its content in the database/)).toBeDefined()

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
      'enterprise:2026.3': { available: false, reason: 'NO_PROVIDER_IMAGE', providerApp: 'seaweedfs', providerTitle: 'SeaweedFS' },
      'enterprise:2026.2': { available: false, reason: 'NO_PROVIDER_IMAGE', providerApp: 'seaweedfs', providerTitle: 'SeaweedFS' },
    })
    await open(edited({ bundleRepo: 'enterprise', bundleKey: '2026.3', contentStorage: 'S3' }))
    await waitForStorage()
    // The provider's name comes from the answer — so it is in.
    await screen.findByText('S3 (SeaweedFS)')
    expect(screen.queryByText(/Saving will be refused/)).toBeNull()

    await act(async () => { screen.getByRole('button', { name: /2026\.3/ }).click() })
    await act(async () => { screen.getByRole('option', { name: '2026.2' }).click() })

    await waitForStorage()
    const warning = await screen.findByText(/Saving will be refused/)
    expect(warning.textContent).toContain('names an image for seaweedfs')
  })
})
