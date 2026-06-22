import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { Modal } from './Modal'

// jsdom has no native <dialog> modal behaviour — stub the methods the hook calls.
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = vi.fn()
  HTMLDialogElement.prototype.close = vi.fn()
})

describe('Modal', () => {
  it('does not bubble a nested modal form submit to the outer modal form', () => {
    const outerSubmit = vi.fn((e: React.FormEvent) => e.preventDefault())
    const innerSubmit = vi.fn((e: React.FormEvent) => e.preventDefault())

    render(
      <Modal open title="Outer" onClose={() => {}} onSubmit={outerSubmit}>
        <Modal open title="Inner" onClose={() => {}} onSubmit={innerSubmit}>
          <button type="submit">create</button>
        </Modal>
      </Modal>,
    )

    fireEvent.click(screen.getByText('create'))

    expect(innerSubmit).toHaveBeenCalledTimes(1)
    // The submit event must NOT reach the outer modal's <form> — otherwise the
    // parent dialog (e.g. the workspace create form) runs its own submit and the
    // whole stack collapses.
    expect(outerSubmit).not.toHaveBeenCalled()
  })
})
