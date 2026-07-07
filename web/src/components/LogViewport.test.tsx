import { describe, it, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/react'
import { createRef } from 'react'
import type { RefObject } from 'react'
import { LogViewport } from './LogViewport'
import type { LogEntry } from '../hooks/useLogStream'

const ENTRIES: LogEntry[] = [
  { id: 0, text: '[INFO] one', level: 'INFO' },
  { id: 1, text: '[INFO] two', level: 'INFO' },
]

function renderViewport(over: Partial<Parameters<typeof LogViewport>[0]> = {}) {
  const parentRef = createRef<HTMLDivElement>() as RefObject<HTMLDivElement | null>
  const selectAllRef = { current: false } as RefObject<boolean>
  const setFollow = vi.fn()
  const onSelectingChange = vi.fn()
  const utils = render(
    <LogViewport
      entries={ENTRIES}
      safeSearchRegex={null}
      matchIndices={[]}
      safeMatchIndex={0}
      searchNavTick={0}
      wordWrap={false}
      compact={false}
      follow={true}
      setFollow={setFollow}
      parentRef={parentRef}
      selectAllRef={selectAllRef}
      onSelectingChange={onSelectingChange}
      {...over}
    />,
  )
  return { parentRef, selectAllRef, setFollow, onSelectingChange, ...utils }
}

describe('LogViewport follow behaviour', () => {
  it('breaks follow on wheel-up (user intent, independent of the scroll gate)', () => {
    const { parentRef, setFollow } = renderViewport()
    fireEvent.wheel(parentRef.current!, { deltaY: -120 })
    expect(setFollow).toHaveBeenCalledWith(false)
  })

  it('does NOT break follow on wheel-down', () => {
    const { parentRef, setFollow } = renderViewport()
    fireEvent.wheel(parentRef.current!, { deltaY: 120 })
    expect(setFollow).not.toHaveBeenCalledWith(false)
  })
})

describe('LogViewport selection behaviour', () => {
  it('signals selection drag on content mousedown and releases on window mouseup', () => {
    const { parentRef, onSelectingChange } = renderViewport()
    const content = parentRef.current!.firstElementChild as HTMLElement
    fireEvent.mouseDown(content)
    expect(onSelectingChange).toHaveBeenLastCalledWith(true)
    fireEvent.mouseUp(window)
    expect(onSelectingChange).toHaveBeenLastCalledWith(false)
  })

  it('cancels select-all mode on content mousedown', () => {
    const { parentRef, selectAllRef } = renderViewport()
    selectAllRef.current = true
    fireEvent.mouseDown(parentRef.current!.firstElementChild as HTMLElement)
    expect(selectAllRef.current).toBe(false)
  })

  it('drops a stale select-all flag when the selection collapses elsewhere', () => {
    const { selectAllRef } = renderViewport()
    selectAllRef.current = true
    // jsdom's default selection has rangeCount 0 → treated as "selection lost".
    fireEvent(document, new Event('selectionchange'))
    expect(selectAllRef.current).toBe(false)
  })
})
