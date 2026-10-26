import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { SecretsUnlockGuard } from './SecretsUnlockGuard'
import { ContentStorageBanner } from './ContentStorageBanner'
import { StartupBanners } from './StartupBanners'
import { getMigrationStatus, unlockSecrets } from '../lib/api'
import { useDashboardStore } from '../lib/store'
import { useSecretsLockStore } from '../lib/secretsLock'

vi.mock('../lib/api', () => ({
  getMigrationStatus: vi.fn(), unlockSecrets: vi.fn(), submitMasterPassword: vi.fn(),
  resetSecrets: vi.fn(), postContentStorageProvision: vi.fn(),
}))
vi.mock('../lib/toast', () => ({ toast: vi.fn() }))
const status = { encrypted: true, locked: true, hasSecrets: true, hasPendingSecrets: false }
const fetchData = vi.fn()
function setProblem(problem = 'PROVIDER_NOT_GENERATED') {
  useDashboardStore.setState({ namespace: {
    id: 'ns', name: 'Test', status: 'STOPPED', bundleRef: '', apps: [],
    contentStorage: { type: 'S3', providerApp: 'seaweedfs', state: 'pending', problem },
  }, fetchData })
}
function renderGuard() { return render(<><SecretsUnlockGuard /><StartupBanners><ContentStorageBanner /></StartupBanners></>) }
async function submit() {
  const dialog = await screen.findByRole('dialog')
  fireEvent.change(dialog.querySelector('input')!, { target: { value: 'fixture-password' } })
  fireEvent.click(screen.getByRole('button', { name: 'Unlock' }))
}
beforeAll(() => {
  HTMLDialogElement.prototype.showModal = vi.fn(function (this: HTMLDialogElement) { this.open = true })
  HTMLDialogElement.prototype.close = vi.fn(function (this: HTMLDialogElement) { this.open = false })
})
beforeEach(() => {
  vi.clearAllMocks()
  useSecretsLockStore.setState(useSecretsLockStore.getInitialState(), true)
  fetchData.mockResolvedValue(undefined)
  vi.mocked(getMigrationStatus).mockResolvedValue(status)
  vi.mocked(unlockSecrets).mockResolvedValue({ success: true, message: '' })
  setProblem()
})
describe('secret-dependent startup warnings', () => {
  it('does not flash an S3 alert before the initial lock check resolves', async () => {
    let resolve!: (s: typeof status) => void
    vi.mocked(getMigrationStatus).mockImplementation(() => new Promise(r => { resolve = r }))
    renderGuard()
    expect(screen.queryByRole('alert')).toBeNull()
    await act(async () => { resolve(status) })
    expect(await screen.findByRole('dialog')).toBeVisible()
    expect(screen.queryByRole('alert')).toBeNull()
  })
  it('keeps the warning gated after an incorrect password', async () => {
    vi.mocked(unlockSecrets).mockRejectedValue(new Error('wrong password'))
    renderGuard()
    await submit()
    await waitFor(() => expect(unlockSecrets).toHaveBeenCalledOnce())
    await waitFor(() => expect(screen.getByRole('button', { name: 'Unlock' })).not.toBeDisabled())
    expect(useSecretsLockStore.getState().ready).toBe(false)
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.getByRole('dialog')).toBeVisible()
  })
  it('waits for refreshed namespace data before releasing the warning gate', async () => {
    let finish!: () => void
    fetchData.mockImplementation(() => new Promise<void>(r => { finish = r }))
    renderGuard()
    await submit()
    await waitFor(() => expect(fetchData).toHaveBeenCalledOnce())
    expect(useSecretsLockStore.getState().ready).toBe(false)
    expect(screen.queryByRole('alert')).toBeNull()
    await act(async () => { setProblem(''); finish() })
    await waitFor(() => expect(useSecretsLockStore.getState().ready).toBe(true))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })
  it('reveals a real configuration error that remains after unlocking', async () => {
    fetchData.mockImplementation(async () => { setProblem('NO_PROVIDER_IMAGE') })
    renderGuard()
    await submit()
    expect(await screen.findByRole('alert')).toBeVisible()
    expect(screen.queryByRole('dialog')).toBeNull()
  })
  it('shows genuine errors after an already-unlocked status check', async () => {
    vi.mocked(getMigrationStatus).mockResolvedValue({ ...status, locked: false })
    renderGuard()
    expect(await screen.findByRole('alert')).toBeVisible()
    expect(screen.queryByRole('dialog')).toBeNull()
  })
  it('does not hide genuine errors forever when the status request fails', async () => {
    vi.mocked(getMigrationStatus).mockRejectedValue(new Error('offline'))
    renderGuard()
    expect(await screen.findByRole('alert')).toBeVisible()
  })
  it('also waits for pending Kotlin secrets even with locked=false', async () => {
    vi.mocked(getMigrationStatus).mockResolvedValue({ ...status, encrypted: false, locked: false, hasPendingSecrets: true })
    renderGuard()
    expect(await screen.findByRole('dialog')).toBeVisible()
    expect(screen.queryByRole('alert')).toBeNull()
  })
})
