import { render, screen, fireEvent } from '@testing-library/react'
import { describe, it, expect, vi, beforeAll } from 'vitest'
import { MasterPasswordDialog } from './MasterPasswordDialog'

beforeAll(() => {
  // jsdom doesn't implement <dialog> showModal/close — stub them.
  HTMLDialogElement.prototype.showModal = vi.fn(function (this: HTMLDialogElement) { this.open = true })
  HTMLDialogElement.prototype.close = vi.fn(function (this: HTMLDialogElement) { this.open = false })
})

/**
 * The validation messages this dialog raises for itself (mismatch, empty
 * password) used to be invisible: `showError` merged the parent's `error` prop
 * with the local one via `??`, which only falls through on null/undefined —
 * and every caller (SecretPicker, SecretsDialog, SecretsUnlockGuard) holds that
 * prop as `useState('')`. An empty string is not nullish, so the merge always
 * yielded `''` and the message was dropped on the floor. On Windows this read
 * as "Confirm does nothing and never says why".
 *
 * These tests pin the callers' real shape: `error=""`, not `error={null}`.
 */
function renderCreate(props: Partial<Parameters<typeof MasterPasswordDialog>[0]> = {}) {
  const onSubmit = vi.fn()
  render(
    <MasterPasswordDialog
      mode="create"
      open
      loading={false}
      error=""
      onSubmit={onSubmit}
      {...props}
    />,
  )
  return { onSubmit }
}

function fields() {
  // Password fields have no accessible role; the confirm field is the second input.
  return document.querySelectorAll('input')
}

describe('MasterPasswordDialog create mode', () => {
  it('shows the mismatch message when the two passwords differ', () => {
    const { onSubmit } = renderCreate()
    const [pwd, confirm] = fields()

    fireEvent.change(pwd, { target: { value: 'correct-horse' } })
    fireEvent.change(confirm, { target: { value: 'battery-staple' } })
    fireEvent.click(screen.getByText('Confirm'))

    expect(screen.getByText('Passwords do not match')).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('submits and raises nothing when the two passwords match', () => {
    const { onSubmit } = renderCreate()
    const [pwd, confirm] = fields()

    fireEvent.change(pwd, { target: { value: 'correct-horse' } })
    fireEvent.change(confirm, { target: { value: 'correct-horse' } })
    fireEvent.click(screen.getByText('Confirm'))

    expect(screen.queryByText('Passwords do not match')).not.toBeInTheDocument()
    expect(onSubmit).toHaveBeenCalledWith('correct-horse')
  })

  it('clears the mismatch message once the confirmation is corrected', () => {
    const { onSubmit } = renderCreate()
    const [pwd, confirm] = fields()

    fireEvent.change(pwd, { target: { value: 'correct-horse' } })
    fireEvent.change(confirm, { target: { value: 'nope' } })
    fireEvent.click(screen.getByText('Confirm'))
    expect(screen.getByText('Passwords do not match')).toBeInTheDocument()

    fireEvent.change(confirm, { target: { value: 'correct-horse' } })
    fireEvent.click(screen.getByText('Confirm'))

    expect(screen.queryByText('Passwords do not match')).not.toBeInTheDocument()
    expect(onSubmit).toHaveBeenCalledWith('correct-horse')
  })

  it('still shows the parent error when the daemon rejects the password', () => {
    renderCreate({ error: 'secrets are locked' })
    expect(screen.getByText('secrets are locked')).toBeInTheDocument()
  })

  // The parent's error survives until its next attempt, and a locally refused
  // click never reaches the parent — so a mismatch typed after a rejected
  // password would be reported as whatever the daemon said the time before.
  it('reports a fresh mismatch rather than the parent error from an earlier attempt', () => {
    const { onSubmit } = renderCreate({ error: 'secrets are locked' })
    const [pwd, confirm] = fields()

    fireEvent.change(pwd, { target: { value: 'correct-horse' } })
    fireEvent.change(confirm, { target: { value: 'battery-staple' } })
    fireEvent.click(screen.getByText('Confirm'))

    expect(screen.getByText('Passwords do not match')).toBeInTheDocument()
    expect(screen.queryByText('secrets are locked')).not.toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })
})
