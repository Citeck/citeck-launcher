import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { DownloadProgress } from './DownloadProgress'

afterEach(() => vi.useRealTimers())
describe('DownloadProgress', () => {
 it('renders real bytes and percentage, then verification without a false percentage', () => {
  const now = Date.now()
  const { rerender } = render(<DownloadProgress progress={{ phase: 'downloading', receivedBytes: 1048576, totalBytes: 4194304, startedAt: now, updatedAt: now }} />)
  expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '25')
  expect(screen.getByText('1.0 MiB / 4.0 MiB · 25%')).toBeInTheDocument()
  rerender(<DownloadProgress progress={{ phase: 'verifying', receivedBytes: 0, totalBytes: 0, startedAt: now, updatedAt: now }} />)
  expect(screen.getByText('Verifying download…')).toBeInTheDocument()
  expect(screen.getByRole('progressbar')).not.toHaveAttribute('aria-valuenow')
 })
 it('shows unknown size and elapsed silence without pretending completion or failure', () => {
  vi.useFakeTimers()
  const now = Date.now()
  render(<DownloadProgress progress={{ phase: 'downloading', receivedBytes: 1048576, totalBytes: -1, startedAt: now, updatedAt: now }} />)
  expect(screen.getByRole('progressbar')).not.toHaveAttribute('aria-valuenow')
  expect(screen.getByText('1.0 MiB')).toBeInTheDocument()
  act(() => vi.advanceTimersByTime(31_000))
  expect(screen.getByText('Elapsed: 31 s')).toBeInTheDocument()
  expect(screen.getByText(/No progress update for 31 s/)).toBeInTheDocument()
 })
})
