import { act, render, screen } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { SnapshotsDialog } from './SnapshotsDialog'
import { getSnapshots, getWorkspaceSnapshots, postSnapshotDownload, postImportSnapshotByName } from '../lib/api'

vi.mock('../lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/api')>()),
  getSnapshots: vi.fn(),
  getWorkspaceSnapshots: vi.fn(),
  postSnapshotDownload: vi.fn(),
  postImportSnapshotByName: vi.fn(),
}))

beforeEach(() => {
  HTMLDialogElement.prototype.showModal = vi.fn(function (this: HTMLDialogElement) { this.open = true })
  HTMLDialogElement.prototype.close = vi.fn(function (this: HTMLDialogElement) { this.open = false })
})

it('disables incompatible workspace downloads and cached snapshot restores with a visible reason', async () => {
  const reason = 'Snapshot demo requires bundle 2026.3 or newer; selected bundle: 2026.2.'
  vi.mocked(getWorkspaceSnapshots).mockResolvedValue([{ id: 'demo', name: 'Demo data', url: 'https://example.invalid/demo.zip', unavailableReason: reason }])
  vi.mocked(getSnapshots).mockResolvedValue([{ name: 'demo.zip', size: 42, createdAt: '2026-11-06T10:00:00Z', unavailableReason: reason }])
  await act(async () => { render(<SnapshotsDialog open namespaceStopped onClose={() => {}} />) })
  expect(await screen.findAllByText(reason)).toHaveLength(2)
  for (const hint of screen.getAllByTitle(reason)) {
    const button = hint.querySelector('button')!
    expect(button.disabled).toBe(true)
    button.click()
  }
  expect(postSnapshotDownload).not.toHaveBeenCalled()
  expect(postImportSnapshotByName).not.toHaveBeenCalled()
})
