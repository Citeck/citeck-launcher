import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { copyText } from './clipboard'
import { toast } from './toast'

vi.mock('./toast', () => ({ toast: vi.fn() }))
vi.mock('./i18n', () => ({ t: (key: string) => key }))

beforeEach(() => {
  vi.clearAllMocks()
  vi.stubGlobal('isSecureContext', true)
  vi.stubGlobal('navigator', { clipboard: undefined })
  Object.defineProperty(document, 'execCommand', { configurable: true, value: vi.fn(() => true) })
})
afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  Reflect.deleteProperty(document, 'execCommand')
  document.body.replaceChildren()
})

function copyButton(host: HTMLElement = document.body) {
  const button = document.createElement('button')
  host.appendChild(button)
  button.focus()
  return button
}

describe('copyText', () => {
  it('prefers the async API without inserting a fallback textarea', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    expect(await copyText('FIXTURE_ASYNC')).toBe(true)
    expect(writeText).toHaveBeenCalledWith('FIXTURE_ASYNC')
    expect(document.execCommand).not.toHaveBeenCalled()
    expect(document.querySelector('textarea')).toBeNull()
  })

  it('keeps the default body fallback for callers outside a dialog and restores focus', async () => {
    const button = copyButton()
    vi.mocked(document.execCommand).mockImplementation((command) => {
      const textarea = document.querySelector('textarea')!
      expect(command).toBe('copy')
      expect(textarea.parentElement).toBe(document.body)
      expect(textarea).toHaveValue('FIXTURE_ENDPOINT')
      expect(textarea).toHaveFocus()
      expect(textarea.selectionEnd - textarea.selectionStart).toBe('FIXTURE_ENDPOINT'.length)
      return true
    })
    expect(await copyText('FIXTURE_ENDPOINT')).toBe(true)
    expect(document.querySelector('textarea')).toBeNull()
    expect(button).toHaveFocus()
    expect(toast).not.toHaveBeenCalled()
  })

  it('uses the supplied dialog context when async writing rejects, then cleans up and restores focus', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('Fixture permission denied'))
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    const dialog = document.createElement('dialog')
    dialog.open = true
    document.body.appendChild(dialog)
    const button = copyButton(dialog)
    vi.mocked(document.execCommand).mockImplementation(() => {
      const textarea = dialog.querySelector('textarea')!
      expect(textarea.parentElement).toBe(dialog)
      expect(textarea).toHaveValue('FIXTURE_DIALOG')
      expect(textarea).toHaveFocus()
      return true
    })
    expect(await copyText('FIXTURE_DIALOG', dialog)).toBe(true)
    expect(writeText).toHaveBeenCalledWith('FIXTURE_DIALOG')
    expect(document.querySelector('textarea')).toBeNull()
    expect(button).toHaveFocus()
    expect(toast).not.toHaveBeenCalled()
  })

  it('skips the async API in a non-secure context', async () => {
    const writeText = vi.fn()
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    vi.stubGlobal('isSecureContext', false)
    expect(await copyText('FIXTURE_HTTP')).toBe(true)
    expect(writeText).not.toHaveBeenCalled()
    expect(document.execCommand).toHaveBeenCalledWith('copy')
  })

  it('refuses false success if the fallback textarea cannot receive focus', async () => {
    const button = copyButton()
    vi.spyOn(HTMLTextAreaElement.prototype, 'focus').mockImplementation(() => {})
    vi.spyOn(HTMLTextAreaElement.prototype, 'select').mockImplementation(() => {})
    expect(await copyText('FIXTURE_INERT')).toBe(false)
    expect(document.execCommand).not.toHaveBeenCalled()
    expect(document.querySelector('textarea')).toBeNull()
    expect(button).toHaveFocus()
    expect(toast).toHaveBeenCalledWith('clipboard.copyFailed', 'error')
  })

  for (const outcome of ['false', 'throw'] as const) {
    it(`reports fallback ${outcome} honestly and restores focus without leaking a textarea`, async () => {
      const button = copyButton()
      vi.mocked(document.execCommand).mockImplementation(() => {
        if (outcome === 'throw') throw new Error('Fixture copy failure')
        return false
      })
      expect(await copyText('FIXTURE_FAILED')).toBe(false)
      expect(document.querySelector('textarea')).toBeNull()
      expect(button).toHaveFocus()
      expect(toast).toHaveBeenCalledWith('clipboard.copyFailed', 'error')
    })
  }

  it('cleans up and reports a selection failure', async () => {
    const button = copyButton()
    vi.spyOn(HTMLTextAreaElement.prototype, 'select').mockImplementation(() => { throw new Error('Fixture selection failure') })
    expect(await copyText('FIXTURE_SELECTION_FAILED')).toBe(false)
    expect(document.querySelector('textarea')).toBeNull()
    expect(button).toHaveFocus()
    expect(toast).toHaveBeenCalledWith('clipboard.copyFailed', 'error')
  })
})
