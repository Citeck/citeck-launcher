import { render, screen } from '@testing-library/react'
import { describe, it, expect } from 'vitest'
import { StatusBadge } from './StatusBadge'

describe('StatusBadge', () => {
  it('renders status text', () => {
    render(<StatusBadge status="RUNNING" />)
    expect(screen.getByText('Running')).toBeInTheDocument()
  })

  it('renders FAILED status', () => {
    render(<StatusBadge status="FAILED" />)
    expect(screen.getByText('Failed')).toBeInTheDocument()
  })

  it('renders unknown status', () => {
    render(<StatusBadge status="UNKNOWN" />)
    // Falls back to the key when no translation exists
    expect(screen.getByText('status.UNKNOWN')).toBeInTheDocument()
  })
})
