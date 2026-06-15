import { useCallback, useEffect, useMemo, useState } from 'react'
import type { LogLevel } from './useLogStream'
import { LOG_LEVELS } from './useLogStream'

// DEBUG is hidden by default — it's high-volume, low-signal for routine viewing
// (e.g. the daemon's per-request lines). The user can toggle it back on.
const DEFAULT_ENABLED_LEVELS: LogLevel[] = LOG_LEVELS.filter((l) => l !== 'DEBUG')

export function escapeRegExp(str: string): string {
  return str.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

// Crude ReDoS guard: a quantified group followed by another quantifier
// ("(a+)+", "(a*){2}"…) can backtrack exponentially — fall back to a literal
// match for such patterns instead of risking a frozen tab.
const NESTED_QUANTIFIER_RE = /([+*]|\{\d)[^)]*\)\s*[+*{]/

export function isRegexSafe(pattern: string): boolean {
  try {
    new RegExp(pattern, 'i')
    return !NESTED_QUANTIFIER_RE.test(pattern)
  } catch {
    return false
  }
}

/**
 * Builds the case-insensitive hide-filter regex from the wildcard filter
 * text: every regex metacharacter except '*' is escaped literally, '*'
 * becomes '.*'. Returns null when the filter is too short (< 2 chars) or the
 * resulting pattern fails to compile — null disables filtering.
 */
export function buildWildcardFilter(filter: string): RegExp | null {
  if (!filter || filter.length < 2) return null
  try {
    const escaped = filter.replace(/[.+?^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*')
    return new RegExp(escaped, 'i')
  } catch {
    return null
  }
}

/**
 * Builds the search regex for the query. Plain mode escapes the query
 * literally. Regex mode compiles the user pattern, but an UNSAFE pattern
 * (NESTED_QUANTIFIER_RE — catastrophic-backtracking shape) degrades to a
 * literal match with a warning, and an INVALID pattern yields no regex at
 * all. Empty query → no search.
 */
export function buildSearchRegex(query: string, useRegex: boolean): { safeSearchRegex: RegExp | null; regexWarning: string | null } {
  if (!query) {
    return { safeSearchRegex: null, regexWarning: null }
  }
  if (useRegex) {
    try {
      new RegExp(query, 'i')
    } catch {
      return { safeSearchRegex: null, regexWarning: null }
    }
    if (!isRegexSafe(query)) {
      return {
        safeSearchRegex: new RegExp(escapeRegExp(query), 'gi'),
        regexWarning: 'Regex too complex — using literal match',
      }
    }
    return { safeSearchRegex: new RegExp(query, 'gi'), regexWarning: null }
  }
  return { safeSearchRegex: new RegExp(escapeRegExp(query), 'gi'), regexWarning: null }
}

/**
 * useLogFilter owns everything between the raw line buffer and the rendered
 * list: level toggles, the wildcard hide-filter, the search query (plain /
 * regex) and search-match navigation.
 *
 * Search navigation model: `searchNavTick` increments only on EXPLICIT user
 * navigation (typing a fresh query, Enter / F3 / ↑↓ buttons). The viewport's
 * scroll-to-match effect listens to this tick instead of `safeMatchIndex` so
 * incidental index resets (e.g. a new chunk shifting matchIndices) don't snap
 * the viewport and lock the user out of free scrolling.
 */
export function useLogFilter(lines: string[], levels: (LogLevel | null)[]) {
  const [search, setSearch] = useState('')
  const [debouncedSearch, setDebouncedSearch] = useState('')
  const [filterText, setFilterText] = useState('')
  const [debouncedFilter, setDebouncedFilter] = useState('')
  const [useRegex, setUseRegex] = useState(false)
  const [enabledLevels, setEnabledLevels] = useState<Set<LogLevel>>(new Set(DEFAULT_ENABLED_LEVELS))
  const [matchIndex, setMatchIndex] = useState(0)
  const [searchNavTick, setSearchNavTick] = useState(0)
  const bumpSearchNav = useCallback(() => setSearchNavTick((t) => t + 1), [])

  // Debounce search input (300ms)
  useEffect(() => {
    const trimmed = search.slice(0, 200)
    const timer = setTimeout(() => setDebouncedSearch(trimmed), 300)
    return () => clearTimeout(timer)
  }, [search])

  useEffect(() => {
    const trimmed = filterText.slice(0, 200)
    const timer = setTimeout(() => setDebouncedFilter(trimmed), 300)
    return () => clearTimeout(timer)
  }, [filterText])

  // Kotlin parity: lines without a parsed level fall into the UNKNOWN bucket
  // and respect its dedicated toggle (LogsViewer.kt:151-158).
  const { filteredLines, filteredLevels, totalLineCount } = useMemo(() => {
    const pattern = buildWildcardFilter(debouncedFilter)
    const entries = lines
      .map((line, i) => ({ line, level: (levels[i] ?? null) as LogLevel | null }))
      .filter(({ line, level }) => {
        const bucket: LogLevel = level ?? 'UNKNOWN'
        if (!enabledLevels.has(bucket)) return false
        if (pattern && !pattern.test(line)) return false
        return true
      })
    return {
      filteredLines: entries.map((e) => e.line),
      filteredLevels: entries.map((e) => e.level),
      totalLineCount: lines.length,
    }
  }, [lines, levels, enabledLevels, debouncedFilter])

  const { safeSearchRegex, regexWarning } = useMemo(
    () => buildSearchRegex(debouncedSearch, useRegex),
    [debouncedSearch, useRegex],
  )

  const matchIndices = useMemo(() => {
    const matches: number[] = []
    if (safeSearchRegex) {
      // Local copy: .test() mutates lastIndex on a /g/ regex, and memoized
      // values must not be mutated (react-compiler contract).
      const re = new RegExp(safeSearchRegex.source, safeSearchRegex.flags)
      filteredLines.forEach((line, i) => {
        re.lastIndex = 0
        if (re.test(line)) matches.push(i)
      })
    }
    return matches
  }, [filteredLines, safeSearchRegex])

  const safeMatchIndex = matchIndices.length > 0
    ? ((matchIndex % matchIndices.length) + matchIndices.length) % matchIndices.length
    : 0

  // Reset matchIndex when the query itself changes (not when matchIndices
  // just shifts due to new chunks). Render-time state adjustment (the
  // React-sanctioned "derive state from props" pattern, same as
  // JournalDialog's selection reset) instead of an effect, so there is no
  // extra post-commit render pass.
  const [prevQuery, setPrevQuery] = useState({ q: debouncedSearch, r: useRegex })
  if (prevQuery.q !== debouncedSearch || prevQuery.r !== useRegex) {
    setPrevQuery({ q: debouncedSearch, r: useRegex })
    setMatchIndex(0)
    if (debouncedSearch) setSearchNavTick((t) => t + 1)
  }

  const toggleLevel = useCallback((level: LogLevel) => {
    setEnabledLevels((prev) => {
      const next = new Set(prev)
      if (next.has(level)) next.delete(level)
      else next.add(level)
      return next
    })
  }, [])

  return {
    search, setSearch,
    filterText, setFilterText,
    useRegex, setUseRegex,
    enabledLevels, toggleLevel,
    filteredLines, filteredLevels, totalLineCount,
    safeSearchRegex, regexWarning,
    matchIndices, safeMatchIndex, setMatchIndex,
    searchNavTick, bumpSearchNav,
  }
}
