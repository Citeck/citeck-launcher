import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { Modal } from './Modal'

// jsdom has no native <dialog> modal behaviour — stub the methods the hook calls.
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = vi.fn()
  HTMLDialogElement.prototype.close = vi.fn()
})

describe('Modal', () => {
  it('portals the dialog to document.body so nested modals never nest <form>s', () => {
    // The real-world bug: Chromium resolves a nested submit button's form owner
    // to the OUTERMOST form. Portaling each dialog to <body> keeps every modal's
    // form standalone. Assert the structural guarantee — the dialog is a direct
    // body child, and an inner modal's form is NOT inside the outer modal's form.
    render(
      <Modal open title="Outer" onClose={() => {}} onSubmit={() => {}}>
        <Modal open title="Inner" onClose={() => {}} onSubmit={() => {}}>
          <button type="submit">create</button>
        </Modal>
      </Modal>,
    )

    const dialogs = document.body.querySelectorAll(':scope > dialog')
    expect(dialogs.length).toBe(2) // both modals are direct body children

    const createBtn = screen.getByText('create') as HTMLButtonElement
    const innerForm = createBtn.closest('form')
    // The button's nearest ancestor form has no <form> ancestor of its own — i.e.
    // forms are not nested, so the submit button binds to its own (inner) form.
    expect(innerForm).not.toBeNull()
    expect(innerForm!.parentElement?.closest('form')).toBeNull()
  })

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
