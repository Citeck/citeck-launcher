import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { GitPullErrorDialog } from './GitPullErrorDialog'
import { linkGitRepoSecret, postGitSkipPull, updateWorkspace } from '../lib/api'
import { useI18nStore } from '../lib/i18n'
import type { GitFailureDto } from '../lib/types'

vi.mock('../lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/api')>()),
  listWorkspaces: vi.fn().mockResolvedValue([{ id: 'ws', authType: 'TOKEN', secretId: 'workspace-token' }]),
  getSecrets: vi.fn().mockResolvedValue([
    { id: 'bundle-token', name: 'Bundle token', type: 'GIT_TOKEN' },
    { id: 'fresh', name: 'Fresh token', type: 'GIT_TOKEN' },
  ]),
  linkGitRepoSecret: vi.fn().mockResolvedValue({ success: true }),
  updateWorkspace: vi.fn(),
  postGitSkipPull: vi.fn(),
}))
vi.mock('./SecretPicker', () => ({
  SecretPicker: ({ value, onChange }: { value: string; onChange: (id: string) => void }) => (
    <select aria-label="Choose secret" value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="bundle-token">Bundle token</option>
      <option value="fresh">Fresh token</option>
    </select>
  ),
}))

const failure: GitFailureDto = {
  repoUrl: 'https://gitlab.example.invalid/bundles.git', workspaceId: 'ws',
  secretId: 'bundle-token', cacheAvailable: true, cacheUpdatedAt: Date.UTC(2026, 9, 9, 10),
}
function open(patch: Partial<GitFailureDto> = {}) {
  const onDecide = vi.fn()
  render(<GitPullErrorDialog open repoUrl={failure.repoUrl} workspaceId="ws"
    failure={{ ...failure, ...patch }} errorMessage="authentication required"
    skipAvailable={false} cancelAvailable onDecide={onDecide} />)
  return onDecide
}
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(linkGitRepoSecret).mockReset().mockResolvedValue({ success: true, message: '' })
  useI18nStore.setState({ locale: 'en' })
  HTMLDialogElement.prototype.showModal = vi.fn(function (this: HTMLDialogElement) { this.open = true })
  HTMLDialogElement.prototype.close = vi.fn(function (this: HTMLDialogElement) { this.open = false })
})

describe('Git repository recovery', () => {
  it('shows the actual bundle credential and successful cache time', async () => {
    const onDecide = open()
    expect(await screen.findByText('Secret in use: Bundle token.')).toBeInTheDocument()
    expect(screen.getByText(/Cache updated:/).textContent).toContain('2026')
    expect(screen.getByRole('button', { name: 'Use cached repository' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Try Again' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Choose another' })).toBeEnabled()
    expect(onDecide).not.toHaveBeenCalled()
  })

  it('uses cache only after an explicit click and never suppresses the host', async () => {
    const onDecide = open()
    fireEvent.click(screen.getByRole('button', { name: 'Use cached repository' }))
    expect(onDecide).toHaveBeenCalledWith('cache')
    expect(postGitSkipPull).not.toHaveBeenCalled()
    expect(linkGitRepoSecret).not.toHaveBeenCalled()
  })

  it('reports an unknown timestamp instead of inventing one', () => {
    open({ cacheUpdatedAt: undefined })
    expect(screen.getByText('Cache updated: time unknown.')).toBeInTheDocument()
  })

  it('disables cache when no usable copy exists but still permits retry', async () => {
    const onDecide = open({ cacheAvailable: false, secretId: undefined })
    expect(screen.getByRole('button', { name: 'Use cached repository' })).toBeDisabled()
    expect(screen.getByText('No usable cached repository is available.')).toBeInTheDocument()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Try Again' })) })
    expect(onDecide).toHaveBeenCalledWith('retry')
  })

  it('binds a replacement to the failed repository rather than changing the workspace token', async () => {
    const onDecide = open()
    fireEvent.click(await screen.findByRole('button', { name: 'Choose another' }))
    fireEvent.change(screen.getByLabelText('Choose secret'), { target: { value: 'fresh' } })
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Save and Retry' })) })
    expect(linkGitRepoSecret).toHaveBeenCalledWith('ws', failure.repoUrl, 'fresh')
    expect(updateWorkspace).not.toHaveBeenCalled()
    expect(onDecide).toHaveBeenCalledWith('retry')
  })

  it('does not retry after the replacement binding failed', async () => {
    vi.mocked(linkGitRepoSecret).mockRejectedValueOnce(new Error('binding could not be saved'))
    const onDecide = open()
    fireEvent.click(await screen.findByRole('button', { name: 'Choose another' }))
    fireEvent.change(screen.getByLabelText('Choose secret'), { target: { value: 'fresh' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save and Retry' }))
    await waitFor(() => expect(screen.getByText('binding could not be saved')).toBeInTheDocument())
    expect(onDecide).not.toHaveBeenCalled()
  })
})

it('retains only selected cache copies within a recovery chain', async () => {
  const { gitRecoveryCache } = await import('../lib/giturl')
  expect(gitRecoveryCache(failure, false)).toBeUndefined()
  expect(gitRecoveryCache(failure, true)).toBe(failure.repoUrl)
  const chained = { ...failure, cachedRepos: ['https://gitlab.example.invalid/workspace.git'] }
  expect(gitRecoveryCache(chained, true)).toEqual([chained.cachedRepos[0], failure.repoUrl])
  expect(gitRecoveryCache(chained, false)).toBe(chained.cachedRepos[0])
})
