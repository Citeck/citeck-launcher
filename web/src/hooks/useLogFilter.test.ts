import { describe, it, expect } from 'vitest'
import { escapeRegExp, isRegexSafe, buildWildcardFilter, buildSearchRegex, filterEntries } from './useLogFilter'
import type { LogEntry, LogLevel } from './useLogStream'
import { LOG_LEVELS } from './useLogStream'

describe('filterEntries (level buckets + wildcard hide-filter over entries)', () => {
  const entries: LogEntry[] = [
    { id: 0, text: '[ERROR] boom', level: 'ERROR' },
    { id: 1, text: '  at Foo.bar', level: 'ERROR' },
    { id: 2, text: '[INFO] hello', level: 'INFO' },
    { id: 3, text: 'no level marker', level: null },
  ]
  const all = new Set<LogLevel>(LOG_LEVELS)

  it('passes everything through with all levels enabled and no pattern', () => {
    expect(filterEntries(entries, all, null)).toEqual(entries)
  })

  it('keeps entry ids intact (virtualizer row identity)', () => {
    const out = filterEntries(entries, new Set<LogLevel>(['ERROR']), null)
    expect(out.map((e) => e.id)).toEqual([0, 1])
  })

  it('routes null-level entries through the UNKNOWN bucket', () => {
    const noUnknown = new Set<LogLevel>(LOG_LEVELS.filter((l) => l !== 'UNKNOWN'))
    expect(filterEntries(entries, noUnknown, null).map((e) => e.id)).toEqual([0, 1, 2])
  })

  it('applies the wildcard pattern to entry text', () => {
    const out = filterEntries(entries, all, buildWildcardFilter('foo*bar'))
    expect(out.map((e) => e.id)).toEqual([1])
  })
})

describe('escapeRegExp', () => {
  const cases: [string, string][] = [
    ['a.b', 'a\\.b'],
    ['(x)+[y]*', '\\(x\\)\\+\\[y\\]\\*'],
    ['plain', 'plain'],
    ['a{2}|b$^?\\', 'a\\{2\\}\\|b\\$\\^\\?\\\\'],
  ]
  it.each(cases)('escapes %j → %j', (input, expected) => {
    expect(escapeRegExp(input)).toBe(expected)
  })

  it('escaped output matches the input literally', () => {
    const re = new RegExp(escapeRegExp('a.b(c)*'))
    expect(re.test('a.b(c)*')).toBe(true)
    expect(re.test('axb(c)*')).toBe(false)
  })
})

describe('isRegexSafe (NESTED_QUANTIFIER_RE fallback)', () => {
  const cases: [string, boolean][] = [
    ['error.*timeout', true],
    ['(abc)|(def)', true],
    ['a+b*c?', true],
    // Catastrophic-backtracking shapes: quantified group followed by a quantifier
    ['(a+)+', false],
    ['(a*)*', false],
    ['(a{2})+', false],
    ['(x+)*y', false],
    // Invalid regex is unsafe by definition
    ['[unclosed', false],
    ['(unbalanced', false],
  ]
  it.each(cases)('%j → %s', (pattern, expected) => {
    expect(isRegexSafe(pattern)).toBe(expected)
  })
})

describe('buildWildcardFilter (escaping + * wildcard)', () => {
  it('returns null for empty / too-short filters', () => {
    expect(buildWildcardFilter('')).toBeNull()
    expect(buildWildcardFilter('a')).toBeNull()
    expect(buildWildcardFilter('*')).toBeNull()
  })

  it('treats * as a wildcard, case-insensitively', () => {
    const re = buildWildcardFilter('err*timeout')!
    expect(re.test('ERROR: connection TIMEOUT')).toBe(true)
    expect(re.test('error timeout')).toBe(true)
    expect(re.test('warn timeout')).toBe(false)
  })

  it('escapes regex metacharacters so they match literally', () => {
    const dot = buildWildcardFilter('a.b')!
    expect(dot.test('a.b')).toBe(true)
    expect(dot.test('axb')).toBe(false)

    const grp = buildWildcardFilter('(main)')!
    expect(grp.test('thread (main) started')).toBe(true)

    const plus = buildWildcardFilter('c++')!
    expect(plus.test('built with c++')).toBe(true)
    expect(plus.test('built with cc')).toBe(false)
  })

  it('combines escaped literals with wildcards', () => {
    const re = buildWildcardFilter('[GET]*200')!
    expect(re.test('[GET] /api/v1/health -> 200')).toBe(true)
    expect(re.test('[POST] /api/v1/health -> 200')).toBe(false)
  })
})

describe('buildSearchRegex (plain / regex / safety fallback)', () => {
  it('empty query yields no regex and no warning', () => {
    expect(buildSearchRegex('', false)).toEqual({ safeSearchRegex: null, regexWarning: null })
    expect(buildSearchRegex('', true)).toEqual({ safeSearchRegex: null, regexWarning: null })
  })

  it('plain mode escapes the query literally with gi flags', () => {
    const { safeSearchRegex, regexWarning } = buildSearchRegex('a.b', false)
    expect(regexWarning).toBeNull()
    expect(safeSearchRegex!.flags).toBe('gi')
    expect(safeSearchRegex!.test('A.B')).toBe(true)
    safeSearchRegex!.lastIndex = 0
    expect(safeSearchRegex!.test('axb')).toBe(false)
  })

  it('regex mode compiles a valid safe pattern as-is', () => {
    const { safeSearchRegex, regexWarning } = buildSearchRegex('err(or)?', true)
    expect(regexWarning).toBeNull()
    expect(safeSearchRegex!.source).toBe('err(or)?')
    expect(safeSearchRegex!.test('ERROR')).toBe(true)
  })

  it('regex mode with an INVALID pattern yields no regex (and no warning)', () => {
    expect(buildSearchRegex('[unclosed', true)).toEqual({ safeSearchRegex: null, regexWarning: null })
  })

  it('regex mode with an UNSAFE pattern degrades to a literal match with a warning', () => {
    const { safeSearchRegex, regexWarning } = buildSearchRegex('(a+)+b', true)
    expect(regexWarning).toMatch(/literal match/)
    // Matches the pattern text literally, not as a regex.
    expect(safeSearchRegex!.test('(a+)+b')).toBe(true)
    safeSearchRegex!.lastIndex = 0
    expect(safeSearchRegex!.test('aaab')).toBe(false)
  })
})
