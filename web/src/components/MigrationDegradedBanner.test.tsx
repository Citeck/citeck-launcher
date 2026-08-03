import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { MigrationDegradedBanner } from './MigrationDegradedBanner'
import { getMigrationStatus } from '../lib/api'

vi.mock('../lib/api', () => ({
  getMigrationStatus: vi.fn(),
}))

const DEGRADED = {
  reason: 'dump error: layout map missing meta.id entry',
  occurredAt: '2026-08-03T10:17:29Z',
  backupPath: '/Users/x/.citeck/storage.db.kotlin-bak',
  workspaces: 2,
  namespaces: 3,
  secrets: 0,
  recoveredRepoUrls: 2,
  lostFields: ['secrets', 'workspace.secretId'],
}

const CLEAN = { hasPendingSecrets: false, encrypted: false, locked: false, hasSecrets: false }

beforeEach(() => {
  vi.mocked(getMigrationStatus).mockReset()
})

describe('MigrationDegradedBanner', () => {
  it('stays hidden after a clean migration', async () => {
    vi.mocked(getMigrationStatus).mockResolvedValue(CLEAN)
    render(<MigrationDegradedBanner />)
    await waitFor(() => expect(getMigrationStatus).toHaveBeenCalled())
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('warns that secrets were not carried over when the migration was degraded', async () => {
    vi.mocked(getMigrationStatus).mockResolvedValue({
      ...CLEAN,
      migrationDegraded: true,
      degradedMigration: DEGRADED,
    })
    render(<MigrationDegradedBanner />)

    const alert = await screen.findByRole('alert')
    // The whole point: the user must learn that credentials are gone, since
    // hasPendingSecrets is false exactly when the fallback ran.
    expect(alert.textContent).toContain('No secrets were carried over')
    // And where their untouched 1.x database is, for support.
    expect(alert.textContent).toContain('/Users/x/.citeck/storage.db.kotlin-bak')
    expect(alert.textContent).toContain('layout map missing meta.id entry')
  })

  it('uses partial-read wording instead of the fallback wording', async () => {
    vi.mocked(getMigrationStatus).mockResolvedValue({
      ...CLEAN,
      migrationDegraded: true,
      degradedMigration: {
        ...DEGRADED,
        reason: 'partial read of storage.db: …',
        partial: true,
        lostEntries: 0,
        lostSubtrees: 1,
        lostFields: ['map.entities/global!workspace'],
      },
    })
    render(<MigrationDegradedBanner />)

    const alert = await screen.findByRole('alert')
    // The store DID open here — claiming otherwise sends the user after the
    // wrong problem.
    expect(alert.textContent).not.toContain('could not be read, so the launcher rebuilt')
    expect(alert.textContent).toContain('The old database opened')
    // Shown without the internal "map." bookkeeping prefix.
    expect(alert.textContent).toContain('entities/global!workspace')
    expect(alert.textContent).not.toContain('map.entities')
  })

  it('can be dismissed', async () => {
    vi.mocked(getMigrationStatus).mockResolvedValue({
      ...CLEAN,
      migrationDegraded: true,
      degradedMigration: DEGRADED,
    })
    render(<MigrationDegradedBanner />)

    fireEvent.click(await screen.findByRole('button', { name: 'Close' }))
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('stays hidden when the status endpoint fails', async () => {
    vi.mocked(getMigrationStatus).mockRejectedValue(new Error('daemon down'))
    render(<MigrationDegradedBanner />)
    await waitFor(() => expect(getMigrationStatus).toHaveBeenCalled())
    expect(screen.queryByRole('alert')).toBeNull()
  })
})
