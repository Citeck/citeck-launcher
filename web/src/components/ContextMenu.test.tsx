import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ContextMenu } from './ContextMenu'

describe('ContextMenu', () => {
  it('renders menu items', () => {
    const items = [
      { label: 'Edit', onClick: vi.fn() },
      { label: 'Delete', onClick: vi.fn(), variant: 'danger' as const },
    ]
    render(<ContextMenu items={items} position={{ x: 100, y: 200 }} onClose={vi.fn()} />)

    expect(screen.getByText('Edit')).toBeDefined()
    expect(screen.getByText('Delete')).toBeDefined()
  })

  it('calls onClick when item clicked', () => {
    const onClick = vi.fn()
    const items = [{ label: 'Edit', onClick }]
    render(<ContextMenu items={items} position={{ x: 0, y: 0 }} onClose={vi.fn()} />)

    fireEvent.click(screen.getByText('Edit'))
    expect(onClick).toHaveBeenCalledOnce()
  })

  it('calls onClose on Escape', () => {
    const onClose = vi.fn()
    render(<ContextMenu items={[{ label: 'X', onClick: vi.fn() }]} position={{ x: 0, y: 0 }} onClose={onClose} />)

    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('disables disabled items', () => {
    const onClick = vi.fn()
    const items = [{ label: 'Disabled', onClick, disabled: true }]
    render(<ContextMenu items={items} position={{ x: 0, y: 0 }} onClose={vi.fn()} />)

    const btn = screen.getByText('Disabled')
    expect(btn.closest('button')?.disabled).toBe(true)
  })
})
