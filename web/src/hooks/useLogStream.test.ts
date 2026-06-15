import { describe, it, expect } from 'vitest'
import {
  detectLevel,
  detectLevelsForLines,
  splitChunkLines,
  appendLinesToBuffer,
  MAX_LOG_LINES,
  type LogBuffer,
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

describe('appendLinesToBuffer (level carry + buffer capping)', () => {
  const empty: LogBuffer = { lines: [], levels: [] }

  it('appends with detected levels and inherits the buffer trailing level', () => {
    const prev: LogBuffer = { lines: ['[WARN] w'], levels: ['WARN'] }
    const next = appendLinesToBuffer(prev, ['  continuation', '[INFO] i'], 100)
    expect(next.lines).toEqual(['[WARN] w', '  continuation', '[INFO] i'])
    expect(next.levels).toEqual(['WARN', 'WARN', 'INFO'])
  })

  it('does not mutate the previous buffer', () => {
    const prev: LogBuffer = { lines: ['[WARN] w'], levels: ['WARN'] }
    appendLinesToBuffer(prev, ['x'], 100)
    expect(prev).toEqual({ lines: ['[WARN] w'], levels: ['WARN'] })
  })

  it('caps the buffer at the tail, dropping the oldest lines and levels together', () => {
    const prev: LogBuffer = {
      lines: ['[ERROR] 1', '2', '3'],
      levels: ['ERROR', 'ERROR', 'ERROR'],
    }
    const next = appendLinesToBuffer(prev, ['[INFO] 4', '5'], 4)
    expect(next.lines).toEqual(['2', '3', '[INFO] 4', '5'])
    expect(next.levels).toEqual(['ERROR', 'ERROR', 'INFO', 'INFO'])
  })

  it('clamps a non-positive tail to a 1-line buffer', () => {
    const next = appendLinesToBuffer(empty, ['a', '[DEBUG] b'], 0)
    expect(next.lines).toEqual(['[DEBUG] b'])
    expect(next.levels).toEqual(['DEBUG'])
  })

  it('honors the MAX_LOG_LINES hard ceiling even with a larger tail', () => {
    const lines = Array.from({ length: MAX_LOG_LINES + 5 }, (_, i) => `line ${i}`)
    const next = appendLinesToBuffer(empty, lines, MAX_LOG_LINES + 1000)
    expect(next.lines).toHaveLength(MAX_LOG_LINES)
    expect(next.levels).toHaveLength(MAX_LOG_LINES)
    expect(next.lines[0]).toBe('line 5')
    expect(next.lines[next.lines.length - 1]).toBe(`line ${MAX_LOG_LINES + 4}`)
  })
})
