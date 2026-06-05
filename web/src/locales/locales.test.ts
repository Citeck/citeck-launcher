import { describe, it, expect } from 'vitest'

// Dynamically import all locale files from this directory
const modules = import.meta.glob<{ default: Record<string, string> }>('./*.ts', { eager: true })

// Build locale map: { en: {...}, ru: {...}, de: {...}, ... }
const locales: Record<string, Record<string, string>> = {}
for (const [path, mod] of Object.entries(modules)) {
  if (path.includes('.test.')) continue // skip this test file
  const name = path.replace('./', '').replace('.ts', '')
  locales[name] = mod.default
}

const en = locales['en']
if (!en) throw new Error('en.ts locale not found')
const enKeys = Object.keys(en).sort()
const otherLocales = Object.entries(locales).filter(([name]) => name !== 'en')

describe('locale completeness', () => {
  it('has at least 2 locales loaded', () => {
    expect(Object.keys(locales).length).toBeGreaterThanOrEqual(2)
  })

  for (const [name, translations] of otherLocales) {
    it(`${name} has all keys from en`, () => {
      const missing = enKeys.filter((k) => !(k in translations))
      expect(missing, `${name} is missing ${missing.length} keys:\n  ${missing.join('\n  ')}`).toEqual([])
    })

    it(`${name} has no extra keys not in en`, () => {
      const extra = Object.keys(translations).filter((k) => !(k in en))
      expect(extra, `${name} has ${extra.length} extra keys:\n  ${extra.join('\n  ')}`).toEqual([])
    })
  }
})

// Keys whose value is intentionally identical to en in one or more locales:
// brand names ("Citeck Core") and "Word: {interpolation}" / loanword format
// strings. Anything multi-word that is identical to en and NOT listed here is
// an untranslated English phrase (the failure mode key-parity can't catch:
// the key exists but its value was never translated).
const IDENTICAL_OK = new Set<string>([
  'appDetail.back', // "← Dashboard"
  'common.error',
  'dashboard.error',
  'dashboard.docker.error',
  'drawer.error',
  'welcome.error',
  'logs.title',
  'logViewer.filter', // "Filter... (*)"
  'snapshots.importFile', // "Import .zip…"
  'table.group.core', // "Citeck Core"
  'table.group.coreExt', // "Citeck Core Extensions"
  'table.group.additional', // "Citeck Additional"
])

// Single-word values (Name, Status, Port, Bundle, Snapshot, Namespace, …) are
// frequently legitimate loanwords/cognates across languages, so we only flag
// MULTI-WORD English phrases left verbatim — that is the real regression class.
describe('locale value completeness (no untranslated English phrases)', () => {
  const isInterpOnly = (v: string) => /^[^A-Za-z]*\{[^}]+\}[^A-Za-z]*$/.test(v.trim())
  for (const [name, t] of otherLocales) {
    it(`${name} has no multi-word value left identical to en`, () => {
      const stubs = enKeys.filter(
        (k) =>
          k in t &&
          t[k] === en[k] &&
          /\s/.test(en[k]) &&
          !isInterpOnly(en[k]) &&
          !IDENTICAL_OK.has(k),
      )
      expect(
        stubs,
        `${name} has ${stubs.length} untranslated English phrase(s):\n  ${stubs.join('\n  ')}`,
      ).toEqual([])
    })
  }
})
