import { afterEach, describe, expect, it, vi } from 'vitest'

interface NativeRuntime {
  invoke: ReturnType<typeof vi.fn>
  dispatchWailsEvent?: (e: { name: string; data?: unknown }) => void
}

const nativeWindow = window as unknown as { _wails?: NativeRuntime }
afterEach(() => { delete nativeWindow._wails; vi.resetModules() })

describe('desktop event runtime handshake', () => {
  it('releases queued snapshot frames only after the subscriber is installed', async () => {
    vi.resetModules()
    const frames = ['snapshot_import', 'snapshot_progress', 'snapshot_complete']
    const runtime: NativeRuntime = { invoke: vi.fn(() => {
      for (const [i, type] of frames.entries()) {
        runtime.dispatchWailsEvent?.({ name: 'daemon:event', data: { type, seq: i + 1 } })
      }
    }) }
    nativeWindow._wails = runtime
    const { connectDesktopEvents } = await import('./desktopEvents')
    const received: string[] = []
    const first = connectDesktopEvents(e => received.push(e.type), vi.fn(), vi.fn(), vi.fn())
    expect(runtime.invoke).toHaveBeenCalledExactlyOnceWith('wails:runtime:ready')
    expect(received).toEqual(frames)
    first.close()
    const second = connectDesktopEvents(vi.fn(), vi.fn(), vi.fn(), vi.fn())
    expect(runtime.invoke).toHaveBeenCalledTimes(1)
    second.close()
  })

  it('does not signal ready when merely inspecting desktop capability', async () => {
    vi.resetModules()
    const invoke = vi.fn()
    nativeWindow._wails = { invoke }
    const { isWailsDesktop } = await import('./desktopEvents')
    expect(isWailsDesktop()).toBe(true)
    expect(invoke).not.toHaveBeenCalled()
  })
})
