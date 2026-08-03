import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { WorkspaceDto } from '../lib/types'
import { WorkspaceSelector } from './WorkspaceSelector'

// Hoisted so the vi.mock factory (itself hoisted to the top of the module) can
// reference it. Real ApiError class so the component's `e instanceof ApiError`
// check works (component + test share this one mocked module instance).
const { api, ApiError } = vi.hoisted(() => {
  class ApiError extends Error {
    status: number
    code: string
    constructor(message: string, status: number, code: string) {
      super(message)
      this.name = 'ApiError'
      this.status = status
      this.code = code
    }
  }
  return {
    ApiError,
    api: {
      ApiError,
      listWorkspaces: vi.fn(),
      createWorkspace: vi.fn(),
      activateWorkspace: vi.fn(),
      updateWorkspace: vi.fn(),
      deleteWorkspace: vi.fn(),
      postWorkspaceUpdate: vi.fn(),
      // Pulled in by GitPullErrorDialog when an activation fails.
      getSecrets: vi.fn(),
      postGitSkipPull: vi.fn(),
    },
  }
})

vi.mock('../lib/api', () => api)
vi.mock('../lib/toast', () => ({ toast: vi.fn() }))
vi.mock('../lib/errorModal', () => ({ showError: vi.fn() }))
vi.mock('../lib/daemonStatus', () => ({ useIsDesktop: () => true }))

const NEW_WS: WorkspaceDto = {
  id: 'ws-new', name: 'Test WS', repoUrl: 'https://example.com/r.git',
  repoBranch: 'main', authType: 'NONE', active: false, namespaces: 0,
}

beforeEach(() => {
  vi.clearAllMocks()
  // jsdom has no native <dialog> modal behaviour. Reflect open/close into the
  // `open` property so the dialog's contents are accessible to role queries
  // (a closed <dialog> is treated as hidden, hiding its inputs).
  HTMLDialogElement.prototype.showModal = vi.fn(function (this: HTMLDialogElement) { this.open = true })
  HTMLDialogElement.prototype.close = vi.fn(function (this: HTMLDialogElement) { this.open = false })
  api.getSecrets.mockResolvedValue([])
})

// Open the dropdown, click "Create workspace…", fill the form, submit.
async function createWorkspaceViaForm() {
  fireEvent.click(screen.getByText('Workspace:', { exact: false }))
  fireEvent.click(await screen.findByText('Create workspace...'))
  const boxes = await screen.findAllByRole('textbox')
  fireEvent.change(boxes[0], { target: { value: 'Test WS' } })             // Name
  fireEvent.change(boxes[1], { target: { value: 'https://example.com/r.git' } }) // Repo URL
  fireEvent.click(screen.getByText('Save'))
}

describe('WorkspaceSelector create flow', () => {
  it('auto-activates the freshly created workspace', async () => {
    api.listWorkspaces.mockResolvedValue([])
    api.createWorkspace.mockResolvedValue(NEW_WS)
    api.activateWorkspace.mockResolvedValue({ ok: true })

    render(<WorkspaceSelector activeId="" onChanged={vi.fn()} />)
    await waitFor(() => expect(api.listWorkspaces).toHaveBeenCalled())

    await createWorkspaceViaForm()

    await waitFor(() => expect(api.createWorkspace).toHaveBeenCalled())
    // Kotlin parity: create switches to the new workspace.
    await waitFor(() => expect(api.activateWorkspace).toHaveBeenCalledWith('ws-new'))
  })

  it('keeps the created workspace in the list when its first activation fails', async () => {
    // Mount sees an empty list; after the create+refresh the new (still inactive)
    // workspace must appear — proving handleActivate's finally-refresh runs even
    // on activation failure.
    api.listWorkspaces.mockResolvedValueOnce([]).mockResolvedValue([NEW_WS])
    api.createWorkspace.mockResolvedValue(NEW_WS)
    api.activateWorkspace.mockRejectedValue(new ApiError('repo sync failed', 500, 'WS_REPO_SYNC_FAILED'))

    render(<WorkspaceSelector activeId="" onChanged={vi.fn()} />)
    await waitFor(() => expect(api.listWorkspaces).toHaveBeenCalledTimes(1))

    await createWorkspaceViaForm()

    await waitFor(() => expect(api.activateWorkspace).toHaveBeenCalled())
    // The finally-refresh re-fetched the list (now containing the new workspace).
    await waitFor(() => expect(api.listWorkspaces).toHaveBeenCalledTimes(2))

    // Reopen the dropdown — the created workspace is present despite the failed switch.
    fireEvent.click(screen.getByText('Workspace:', { exact: false }))
    await waitFor(() => expect(screen.getAllByText('Test WS').length).toBeGreaterThan(0))
  })
})

const ACTIVE_WS: WorkspaceDto = {
  id: 'ws-1', name: 'Citeck', repoUrl: 'https://example.com/ws.git',
  repoBranch: 'main', repoPullPeriod: 'PT2H', authType: 'NONE', active: true, namespaces: 2,
}

describe('WorkspaceSelector discoverability', () => {
  it('does not hide the per-row edit/delete actions behind hover', async () => {
    api.listWorkspaces.mockResolvedValue([ACTIVE_WS])
    render(<WorkspaceSelector activeId="ws-1" onChanged={vi.fn()} />)
    await waitFor(() => expect(api.listWorkspaces).toHaveBeenCalled())

    fireEvent.click(screen.getByText('Workspace:', { exact: false }))
    const edit = await screen.findByRole('button', { name: 'Edit' })
    // opacity-0 made the pencil invisible until the pointer happened to land on
    // the row — users reported never finding the git settings at all.
    expect(edit.parentElement?.className ?? '').not.toContain('opacity-0')
  })

  it('offers an explicit workspace-settings row in the dropdown', async () => {
    api.listWorkspaces.mockResolvedValue([ACTIVE_WS])
    render(<WorkspaceSelector activeId="ws-1" onChanged={vi.fn()} />)
    await waitFor(() => expect(api.listWorkspaces).toHaveBeenCalled())

    fireEvent.click(screen.getByText('Workspace:', { exact: false }))
    fireEvent.click(await screen.findByText('Workspace settings...'))
    // The typed form, not the raw workspace-v1.yml editor.
    expect(await screen.findByText('Repository URL')).toBeInTheDocument()
  })

  it('the always-visible gear opens the typed form, not the raw YAML editor', async () => {
    api.listWorkspaces.mockResolvedValue([ACTIVE_WS])
    render(<WorkspaceSelector activeId="ws-1" onChanged={vi.fn()} />)
    await waitFor(() => expect(api.listWorkspaces).toHaveBeenCalled())

    fireEvent.click(screen.getByRole('button', { name: 'Workspace settings' }))
    expect(await screen.findByText('Repository URL')).toBeInTheDocument()
    expect(screen.queryByText('Workspace configuration')).not.toBeInTheDocument()
  })
})
