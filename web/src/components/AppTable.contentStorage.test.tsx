import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { MemoryRouter } from 'react-router'
import { AppTable } from './AppTable'
import { useDashboardStore } from '../lib/store'
import { postAppStop } from '../lib/api'
import type { AppDto, ContentStorageDto, NamespaceDto } from '../lib/types'

vi.mock('../lib/api', () => ({
  postAppStop: vi.fn().mockResolvedValue({ success: true, message: '' }),
  postAppStart: vi.fn().mockResolvedValue({ success: true, message: '' }),
  postAppRestart: vi.fn().mockResolvedValue({ success: true, message: '' }),
  getAppFiles: vi.fn().mockResolvedValue([]),
}))
vi.mock('../lib/toast', () => ({ toast: vi.fn() }))

const apps: AppDto[] = [
  { name: 'seaweedfs', status: 'RUNNING', image: 'chrislusf/seaweedfs:4.48', cpu: '', memory: '', kind: 'THIRD_PARTY', edited: false, locked: false },
  { name: 'emodel', status: 'RUNNING', image: 'ecos-model:2.40', cpu: '', memory: '', kind: 'CITECK_CORE', edited: false, locked: false },
]

function setNamespace(cs: ContentStorageDto | undefined, id = 'n') {
  useDashboardStore.setState({
    namespace: { id, name: id, status: 'RUNNING', bundleRef: '', apps, contentStorage: cs } as NamespaceDto,
  })
}

function stopButton(app: string) {
  const row = screen.getByText(app).closest('tr')!
  return row.querySelector('button[title]') as HTMLButtonElement
}

const s3: ContentStorageDto = { type: 'S3', providerApp: 'seaweedfs', state: 'connected' }

beforeEach(() => {
  // jsdom doesn't implement <dialog> showModal/close — stub them.
  HTMLDialogElement.prototype.showModal = vi.fn(function (this: HTMLDialogElement) { this.open = true })
  HTMLDialogElement.prototype.close = vi.fn(function (this: HTMLDialogElement) { this.open = false })
  vi.mocked(postAppStop).mockClear()
})

describe('AppTable: stopping the S3 storage provider', () => {
  // Owner ruling: warn, never refuse. ecos-content keeps its files in the
  // provider, so stopping it breaks uploads and downloads — and nothing in the
  // app's name says so.
  it('asks before stopping the provider of an S3 namespace, and stops it on confirm', async () => {
    setNamespace(s3)
    render(<MemoryRouter><AppTable apps={apps} /></MemoryRouter>)

    fireEvent.click(stopButton('seaweedfs'))
    expect(postAppStop).not.toHaveBeenCalled()
    const dialog = await screen.findByRole('dialog')
    expect(dialog.textContent).toContain('Stop seaweedfs?')
    expect(dialog.textContent).toContain('uploading and downloading files will fail')

    await act(async () => { screen.getByRole('button', { name: 'Stop' }).click() })
    await waitFor(() => expect(postAppStop).toHaveBeenCalledWith('seaweedfs'))
  })

  it('stops nothing when the question is cancelled', async () => {
    setNamespace(s3)
    render(<MemoryRouter><AppTable apps={apps} /></MemoryRouter>)

    fireEvent.click(stopButton('seaweedfs'))
    await screen.findByRole('dialog')
    await act(async () => { screen.getByRole('button', { name: 'Cancel' }).click() })
    expect(postAppStop).not.toHaveBeenCalled()
  })

  // Every other stop keeps firing immediately (the table dropped its blanket
  // confirmation on purpose).
  it('stops any other app at once', async () => {
    setNamespace(s3)
    render(<MemoryRouter><AppTable apps={apps} /></MemoryRouter>)

    fireEvent.click(stopButton('emodel'))
    await waitFor(() => expect(postAppStop).toHaveBeenCalledWith('emodel'))
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('does not ask on a DB namespace, even for an app of the same name', async () => {
    setNamespace(undefined)
    render(<MemoryRouter><AppTable apps={apps} /></MemoryRouter>)

    fireEvent.click(stopButton('seaweedfs'))
    await waitFor(() => expect(postAppStop).toHaveBeenCalledWith('seaweedfs'))
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})
