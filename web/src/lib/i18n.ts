import { useMemo } from 'react'
import { create } from 'zustand'
import { putUIPrefs } from './api'
import en from '../locales/en'
import ru from '../locales/ru'
import zh from '../locales/zh'
import es from '../locales/es'
import de from '../locales/de'
import fr from '../locales/fr'
import pt from '../locales/pt'
import ja from '../locales/ja'

export type Locale = 'en' | 'ru' | 'zh' | 'es' | 'de' | 'fr' | 'pt' | 'ja'

/**
 * The full key set is derived from the English locale (source of truth).
 * t() only accepts these keys, so a typo or a removed key is a compile error
 * at every static call site.
 */
export type LocaleKey = keyof typeof en

/**
 * A complete translation map. The 7 non-en locales declare
 * `satisfies Translations` so a missing or extra key is ALSO a compile error
 * (the runtime parity test in locales.test.ts stays as a second line of
 * defense for tooling that bypasses tsc).
 */
export type Translations = Record<LocaleKey, string>

export interface LocaleMeta {
  code: Locale
  name: string      // native name
  flag: string      // emoji flag
}

export const LOCALES: LocaleMeta[] = [
  { code: 'en', name: 'English', flag: '🇺🇸' },
  { code: 'ru', name: 'Русский', flag: '🇷🇺' },
  { code: 'zh', name: '简体中文', flag: '🇨🇳' },
  { code: 'es', name: 'Español', flag: '🇪🇸' },
  { code: 'de', name: 'Deutsch', flag: '🇩🇪' },
  { code: 'fr', name: 'Français', flag: '🇫🇷' },
  { code: 'pt', name: 'Português', flag: '🇧🇷' },
  { code: 'ja', name: '日本語', flag: '🇯🇵' },
]

const STORAGE_KEY = 'citeck-locale'

// All locales bundled synchronously — ~30 KB gzipped, no loading flash.
const allTranslations: Record<Locale, Translations> = { en, ru, zh, es, de, fr, pt, ja }

function detectLocale(): Locale {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored && stored in allTranslations) return stored as Locale
  } catch { /* ignore */ }
  const lang = navigator.language.split('-')[0]
  if (lang in allTranslations) return lang as Locale
  return 'en'
}

interface I18nState {
  locale: Locale
  translations: Translations
  setLocale: (locale: Locale, persist?: boolean) => void
}

const initialLocale = detectLocale()

export const useI18nStore = create<I18nState>((set, get) => ({
  locale: initialLocale,
  translations: allTranslations[initialLocale],

  setLocale: (locale: Locale, persist = true) => {
    if (locale === get().locale) return
    try {
      localStorage.setItem(STORAGE_KEY, locale)
    } catch { /* ignore */ }
    // Persist server-side too so a desktop webview localStorage wipe doesn't
    // reset the language. persist=false when applying a value that just came
    // FROM the server (App bootstrap) to avoid echoing it straight back.
    if (persist) void putUIPrefs({ locale })
    set({ locale, translations: allTranslations[locale] })
  },
}))

function translate(translations: Translations, key: LocaleKey, params?: Record<string, string | number>): string {
  // Indexed as a partial record: tDynamic() funnels runtime-assembled keys
  // through here, and those may be absent — fall back to en, then to the
  // raw key (the pre-typing behavior).
  const lookup = translations as Partial<Record<string, string>>
  let text = lookup[key] ?? (en as Partial<Record<string, string>>)[key] ?? key
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      text = text.replace(`{${k}}`, String(v))
    }
  }
  return text
}

/**
 * Translation function. Supports simple interpolation: t('key', { name: 'value' })
 * Replaces {name} placeholders in the translated string.
 */
export function t(key: LocaleKey, params?: Record<string, string | number>): string {
  return translate(useI18nStore.getState().translations, key, params)
}

/**
 * Sanctioned escape hatch for keys assembled at RUNTIME, where the key set
 * cannot be proven at compile time:
 *   - 'status.' + status        (app/namespace status strings from the daemon)
 *   - link descriptionKey       (Go-sourced 'links.*' keys in NamespaceDto)
 *   - KIND_I18N[kind] ?? kind   (unknown app kinds fall back to the raw kind)
 * Unknown keys render as the key itself — same fallback t() always had.
 * Do NOT use this for keys known at compile time; use t() so typos are
 * caught by tsc.
 */
export function tDynamic(key: string, params?: Record<string, string | number>): string {
  return translate(useI18nStore.getState().translations, key as LocaleKey, params)
}

/**
 * React hook version — triggers re-render when locale changes.
 * Returns t()/tDynamic() functions bound to the current translations.
 */
export function useTranslation() {
  const { translations, locale } = useI18nStore()
  // Stable identities per locale so t/tDynamic can appear in hook dependency
  // arrays (useMemo'd form specs etc.) without re-firing on every render.
  const tf = useMemo(
    () => (key: LocaleKey, params?: Record<string, string | number>): string =>
      translate(translations, key, params),
    [translations],
  )
  // Same escape hatch as the module-level tDynamic (see its doc comment),
  // bound to the hook's translations so locale changes re-render.
  const tDyn = useMemo(
    () => (key: string, params?: Record<string, string | number>): string =>
      translate(translations, key as LocaleKey, params),
    [translations],
  )
  return { t: tf, tDynamic: tDyn, locale }
}
