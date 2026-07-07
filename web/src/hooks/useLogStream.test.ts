import { describe, it, expect, vi, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import {
  detectLevel,
  detectLevelsForLines,
  splitChunkLines,
  makeEntries,
  appendEntriesToBuffer,
  effectiveCap,
  overlapLineCount,
  useLogStream,
  MAX_LOG_LINES,
  LOG_FLUSH_INTERVAL_MS,
  type LogEntry,
  type LogLevel,
} from './useLogStream'

describe('detectLevel', () => {
  // One case per LEVEL_PATTERNS entry + misses, table-driven.
  const cases: [string, LogLevel | null][] = [
    // 1. [LEVEL]
    ['[ERROR] connection refused', 'ERROR'],
    ['2026-01-01 [warn] low disk', 'WARN'],
    // 2. logback "|-LEVEL"
    ['12:00:00,123 |-INFO in ch.qos.logback', 'INFO'],
    // 3. HH:MM:SS LEVEL
    ['10:11:12.345 DEBUG starting worker', 'DEBUG'],
    ['10:11:12 TRACE fine-grained', 'TRACE'],
    // 4. ISO timestamp LEVEL
    ['2026-04-01T02:58:51Z INFO  Message key=value', 'INFO'],
    ['2026-04-01T02:58:51.123+03:00 ERROR boom', 'ERROR'],
    // 5. ^LEVEL:
    ['WARN: something odd', 'WARN'],
    // 6. whitespace-delimited LEVEL
    ['main thread ERROR while processing', 'ERROR'],
    // 7. ^LEVEL followed by space/colon/dash/bracket
    ['ERROR[main] handler crashed', 'ERROR'],
    ['DEBUG- verbose details', 'DEBUG'],
    // No recognizable marker
    ['	at java.base/java.lang.Thread.run(Thread.java:833)', null],
    ['{"json": "payload continuation"}', null],
    ['', null],
  ]
  it.each(cases)('detects %j → %s', (line, expected) => {
    expect(detectLevel(line)).toBe(expected)
  })
})

describe('detectLevelsForLines (carry-forward inheritance)', () => {
  it('continuation lines inherit the preceding line level', () => {
    const lines = [
      '[ERROR] kaboom',
      '  at Foo.bar(Foo.java:1)',
      '  at Foo.baz(Foo.java:2)',
      '[INFO] recovered',
      'details follow',
    ]
    expect(detectLevelsForLines(lines, null)).toEqual(['ERROR', 'ERROR', 'ERROR', 'INFO', 'INFO'])
  })

  it('seeds leading continuation lines with the carry level', () => {
    expect(detectLevelsForLines(['  cont 1', '[WARN] w', '  cont 2'], 'DEBUG'))
      .toEqual(['DEBUG', 'WARN', 'WARN'])
  })

  it('keeps null when there is no carry and no marker yet', () => {
    expect(detectLevelsForLines(['plain', '[TRACE] t'], null)).toEqual([null, 'TRACE'])
  })

  it('returns empty for empty input', () => {
    expect(detectLevelsForLines([], 'INFO')).toEqual([])
  })
})

describe('splitChunkLines (trailing partial line handling)', () => {
  const cases: [string, string, string[], string][] = [
    // pending, chunk, complete, nextPending
    ['', 'a\nb\n', ['a', 'b'], ''],
    ['', 'a\nb', ['a'], 'b'],
    ['par', 'tial\nrest', ['partial'], 'rest'],
    ['', 'no-newline-yet', [], 'no-newline-yet'],
    ['x', '\n', ['x'], ''],
    ['', '', [], ''],
    ['', '\n\n', ['', ''], ''],
  ]
  it.each(cases)('pending=%j + chunk=%j → complete=%j, pending=%j', (pending, chunk, complete, nextPending) => {
    expect(splitChunkLines(pending, chunk)).toEqual({ complete, pending: nextPending })
  })
})

describe('makeEntries (id assignment + level carry)', () => {
  it('assigns sequential ids from startId and carries levels forward', () => {
    const entries = makeEntries(['[ERROR] boom', '  at Foo.bar', '[INFO] ok'], null, 10)
    expect(entries).toEqual([
      { id: 10, text: '[ERROR] boom', level: 'ERROR' },
      { id: 11, text: '  at Foo.bar', level: 'ERROR' },
      { id: 12, text: '[INFO] ok', level: 'INFO' },
    ])
  })

  it('seeds leading continuation lines with the carry level', () => {
    const entries = makeEntries(['cont'], 'DEBUG', 0)
    expect(entries).toEqual([{ id: 0, text: 'cont', level: 'DEBUG' }])
  })
})

describe('appendEntriesToBuffer (level carry + capping with stable ids)', () => {
  const warn: LogEntry[] = [{ id: 0, text: '[WARN] w', level: 'WARN' }]

  it('appends with detected levels and inherits the buffer trailing level', () => {
    const next = appendEntriesToBuffer(warn, ['  continuation', '[INFO] i'], 1, 100)
    expect(next).toEqual([
      { id: 0, text: '[WARN] w', level: 'WARN' },
      { id: 1, text: '  continuation', level: 'WARN' },
      { id: 2, text: '[INFO] i', level: 'INFO' },
    ])
  })

  it('does not mutate the previous buffer', () => {
    appendEntriesToBuffer(warn, ['x'], 1, 100)
    expect(warn).toEqual([{ id: 0, text: '[WARN] w', level: 'WARN' }])
  })

  it('caps the buffer, dropping the oldest entries but KEEPING survivor ids', () => {
    const prev = makeEntries(['[ERROR] 1', '2', '3'], null, 0)
    const next = appendEntriesToBuffer(prev, ['[INFO] 4', '5'], 3, 4)
    expect(next.map((e) => e.text)).toEqual(['2', '3', '[INFO] 4', '5'])
    // Ids follow the LINES, not the buffer slots — this is what lets the
    // virtualizer keep row identity across front-trims.
    expect(next.map((e) => e.id)).toEqual([1, 2, 3, 4])
  })

  it('clamps a non-positive cap to a 1-line buffer', () => {
    const next = appendEntriesToBuffer([], ['a', '[DEBUG] b'], 0, 0)
    expect(next).toEqual([{ id: 1, text: '[DEBUG] b', level: 'DEBUG' }])
  })
})

describe('effectiveCap (freeze-on-unfollow window)', () => {
  it('caps at the tail while following', () => {
    expect(effectiveCap(500, true)).toBe(500)
    expect(effectiveCap(0, true)).toBe(1)
    expect(effectiveCap(MAX_LOG_LINES + 1000, true)).toBe(MAX_LOG_LINES)
  })

  it('caps only at the safety ceiling while NOT following (frozen window)', () => {
    expect(effectiveCap(500, false)).toBe(MAX_LOG_LINES)
  })
})

describe('overlapLineCount (backlog↔live seam dedup)', () => {
  const cases: [string, string[], string[], number][] = [
    ['no overlap', ['a', 'b'], ['c', 'd'], 0],
    ['single-line overlap', ['a', 'b'], ['b', 'c'], 1],
    ['multi-line overlap', ['a', 'b', 'c'], ['b', 'c', 'd'], 2],
    ['held fully contained in backlog tail', ['a', 'b', 'c'], ['b', 'c'], 2],
    ['empty held', ['a'], [], 0],
    ['empty backlog', [], ['a'], 0],
    // Repeated identical lines: prefer the LARGEST overlap — dropping a
    // legit duplicate is better than replaying the whole run of dupes.
    ['identical repeated lines', ['ping', 'ping'], ['ping', 'ping', 'pong'], 2],
  ]
  it.each(cases)('%s', (_name, prev, next, expected) => {
    expect(overlapLineCount(prev, next)).toBe(expected)
  })
})

// ---------------------------------------------------------------------------
// Hook-level behaviour: coalescing, freeze-on-unfollow, selection pause.
// ---------------------------------------------------------------------------

type ReadResult = { done: boolean; value?: Uint8Array }

/** Controllable fake for the live-tail response body. */
function fakeStreamBody() {
  const encoder = new TextEncoder()
  const queue: ReadResult[] = []
  const waiters: ((r: ReadResult) => void)[] = []
  const emit = (r: ReadResult) => {
    const w = waiters.shift()
    if (w) w(r)
    else queue.push(r)
  }
  return {
    push: (text: string) => emit({ done: false, value: encoder.encode(text) }),
    close: () => emit({ done: true }),
    body: {
      getReader: () => ({
        read: (): Promise<ReadResult> => {
          const q = queue.shift()
          return q ? Promise.resolve(q) : new Promise<ReadResult>((res) => waiters.push(res))
        },
      }),
    },
  }
}

function stubLogFetch(backlog: string) {
  const stream = fakeStreamBody()
  vi.stubGlobal('fetch', vi.fn(async (url: RequestInfo | URL) => {
    if (String(url).includes('follow=true')) {
      return { ok: true, body: stream.body } as unknown as Response
    }
    return { ok: true, text: async () => backlog } as unknown as Response
  }))
  return stream
}

describe('useLogStream (hook behaviour)', () => {
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  async function setup(opts: { backlog?: string; follow?: boolean; paused?: boolean; tail?: number } = {}) {
    vi.useFakeTimers()
    const stream = stubLogFetch(opts.backlog ?? 'l1\nl2\n')
    const hook = renderHook(
      (p: { follow: boolean; paused: boolean; tail: number }) =>
        useLogStream({ appName: 'app', tail: p.tail, follow: p.follow, paused: p.paused }),
      { initialProps: { follow: opts.follow ?? true, paused: opts.paused ?? false, tail: opts.tail ?? 3 } },
    )
    // Let the backlog fetch + live stream start resolve.
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    return { stream, hook, texts: () => hook.result.current.entries.map((e) => e.text) }
  }

  it('loads the backlog and coalesces live chunks into one flush per interval', async () => {
    const { stream, texts } = await setup()
    expect(texts()).toEqual(['l1', 'l2'])

    await act(async () => {
      stream.push('a\n')
      stream.push('b\n')
      await vi.advanceTimersByTimeAsync(0)
    })
    // Chunks received but NOT yet applied — they wait for the flush interval.
    expect(texts()).toEqual(['l1', 'l2'])

    await act(async () => { await vi.advanceTimersByTimeAsync(LOG_FLUSH_INTERVAL_MS) })
    // Both chunks land in ONE flush; tail=3 keeps the newest 3 lines.
    expect(texts()).toEqual(['l2', 'a', 'b'])
  })

  it('keeps ids monotonic across front-trims', async () => {
    const { stream, hook } = await setup({ tail: 3 })
    await act(async () => {
      stream.push('a\nb\nc\n')
      await vi.advanceTimersByTimeAsync(LOG_FLUSH_INTERVAL_MS)
    })
    // 5 lines total, tail=3 → newest 3 survive with their original ids 2,3,4.
    expect(hook.result.current.entries.map((e) => e.text)).toEqual(['a', 'b', 'c'])
    expect(hook.result.current.entries.map((e) => e.id)).toEqual([2, 3, 4])
  })

  it('freezes the window while NOT following and re-trims on follow resume', async () => {
    const { stream, hook } = await setup({ follow: false, tail: 3 })
    await act(async () => {
      stream.push('a\nb\nc\nd\n')
      await vi.advanceTimersByTimeAsync(LOG_FLUSH_INTERVAL_MS)
    })
    // follow=false → buffer grows past the tail (frozen window, no front-trim).
    expect(hook.result.current.entries.map((e) => e.text)).toEqual(['l1', 'l2', 'a', 'b', 'c', 'd'])

    hook.rerender({ follow: true, paused: false, tail: 3 })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    // Resuming follow re-applies the tail cap, keeping the newest lines.
    expect(hook.result.current.entries.map((e) => e.text)).toEqual(['b', 'c', 'd'])
    expect(hook.result.current.entries.map((e) => e.id)).toEqual([3, 4, 5])
  })

  it('defers flushes while paused (selection drag) and applies them on unpause', async () => {
    const { stream, hook, texts } = await setup({ paused: true, tail: 10 })
    await act(async () => {
      stream.push('a\nb\n')
      await vi.advanceTimersByTimeAsync(LOG_FLUSH_INTERVAL_MS * 3)
    })
    // Paused: nothing applied no matter how much time passes.
    expect(texts()).toEqual(['l1', 'l2'])

    hook.rerender({ follow: true, paused: false, tail: 10 })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(texts()).toEqual(['l1', 'l2', 'a', 'b'])
  })

  it('does not lose lines emitted in the backlog→live gap (held + deduped)', async () => {
    vi.useFakeTimers()
    const stream = fakeStreamBody()
    // Backlog text resolution is DEFERRED so the live stream can emit first —
    // exactly the gap the seam-merge covers.
    let resolveBacklog!: (text: string) => void
    const backlogText = new Promise<string>((res) => { resolveBacklog = res })
    vi.stubGlobal('fetch', vi.fn(async (url: RequestInfo | URL) => {
      if (String(url).includes('follow=true')) {
        return { ok: true, body: stream.body } as unknown as Response
      }
      return { ok: true, text: () => backlogText } as unknown as Response
    }))
    const hook = renderHook(() => useLogStream({ appName: 'app', tail: 10 }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })

    // Live stream delivers a line that is ALSO the backlog tail (overlap) and
    // a genuine gap line, all before the backlog resolves.
    await act(async () => {
      stream.push('l2\ngap\n')
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(hook.result.current.entries).toEqual([]) // held, not applied yet

    await act(async () => {
      resolveBacklog('l1\nl2\n')
      await vi.advanceTimersByTimeAsync(LOG_FLUSH_INTERVAL_MS)
    })
    // 'l2' deduped (seam overlap), 'gap' preserved.
    expect(hook.result.current.entries.map((e) => e.text)).toEqual(['l1', 'l2', 'gap'])
  })

  it('applies held live lines even when the backlog request fails', async () => {
    vi.useFakeTimers()
    const stream = fakeStreamBody()
    vi.stubGlobal('fetch', vi.fn(async (url: RequestInfo | URL) => {
      if (String(url).includes('follow=true')) {
        return { ok: true, body: stream.body } as unknown as Response
      }
      return { ok: false, status: 500 } as unknown as Response
    }))
    const hook = renderHook(() => useLogStream({ appName: 'app', tail: 10 }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    await act(async () => {
      stream.push('a\nb\n')
      await vi.advanceTimersByTimeAsync(LOG_FLUSH_INTERVAL_MS)
    })
    expect(hook.result.current.entries.map((e) => e.text)).toEqual(['a', 'b'])
  })

  it('clear() empties the buffer and drops pending unflushed lines', async () => {
    const { stream, hook, texts } = await setup({ tail: 10 })
    await act(async () => {
      stream.push('a\n')
      // Let the chunk land in the pending coalescing buffer (no flush yet).
      await vi.advanceTimersByTimeAsync(0)
    })
    await act(async () => {
      hook.result.current.clear()
      await vi.advanceTimersByTimeAsync(LOG_FLUSH_INTERVAL_MS * 2)
    })
    expect(texts()).toEqual([])
  })
})
