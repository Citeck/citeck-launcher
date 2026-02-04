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
