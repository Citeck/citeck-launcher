import { act, render, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { EditorView } from '@codemirror/view'
import { CodeEditor } from './CodeEditor'
import { useThemeStore } from '../lib/theme'

describe('CodeEditor theme integration', () => {
  it('applies the actual editor dark facet and switches it with the app theme', async () => {
    useThemeStore.getState().setDark(true, false)
    const { container } = render(<CodeEditor value={'name: example\n'} filename="namespace.yml" />)
    const view = () => EditorView.findFromDOM(container.querySelector('.cm-editor')!)!
    await waitFor(() => expect(view().state.facet(EditorView.darkTheme)).toBe(true))
    await act(async () => { useThemeStore.getState().setDark(false, false) })
    await waitFor(() => expect(view().state.facet(EditorView.darkTheme)).toBe(false))
    await act(async () => { useThemeStore.getState().setDark(true, false) })
    await waitFor(() => expect(view().state.facet(EditorView.darkTheme)).toBe(true))
  })
})
