import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect } from 'vitest'
import { Select, type SelectOption } from './Select'

const OPTS: SelectOption[] = [
  { label: 'Community', value: 'community' },
  { label: 'Enterprise', value: 'enterprise' },
]

describe('Select portal target', () => {
  it('portals the popup into the nearest OPEN <dialog> ancestor', () => {
    // Regression: a modal <dialog> (showModal) is promoted to the browser top
    // layer and makes everything outside its subtree inert + click-intercepted
    // by the backdrop. A popup portaled into <body> is then unreachable, which
    // broke every Select inside the create/edit-namespace FormDialog (bundle
    // repo, version, auth type). The popup must land inside the open dialog.
    render(
      <dialog open data-testid="dlg">
        <Select value="" options={OPTS} onChange={() => {}} placeholder="pick" />
      </dialog>,
    )
    fireEvent.click(screen.getByRole('button', { name: /pick/i }))
    const listbox = screen.getByRole('listbox')
    expect(listbox.closest('dialog')).toBe(screen.getByTestId('dlg'))
  })

  it('portals the popup into <body> when there is no open dialog ancestor', () => {
    render(<Select value="" options={OPTS} onChange={() => {}} placeholder="pick" />)
    fireEvent.click(screen.getByRole('button', { name: /pick/i }))
    const listbox = screen.getByRole('listbox')
    expect(listbox.closest('dialog')).toBeNull()
    expect(listbox.parentElement).toBe(document.body)
  })

  it('selecting an option inside a dialog fires onChange', () => {
    let picked = ''
    render(
      <dialog open>
        <Select value="" options={OPTS} onChange={(v) => { picked = v }} placeholder="pick" />
      </dialog>,
    )
    fireEvent.click(screen.getByRole('button', { name: /pick/i }))
    fireEvent.click(screen.getByRole('option', { name: 'Enterprise' }))
    expect(picked).toBe('enterprise')
  })
})
