import { create } from 'zustand'
import en from '../locales/en'
import ru from '../locales/ru'
import zh from '../locales/zh'
import es from '../locales/es'
import de from '../locales/de'
import fr from '../locales/fr'
import pt from '../locales/pt'
import ja from '../locales/ja'

export type Locale = 'en' | 'ru' | 'zh' | 'es' | 'de' | 'fr' | 'pt' | 'ja'

export type Translations = Record<string, string>

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
  setLocale: (locale: Locale) => void
}

const initialLocale = detectLocale()

export const useI18nStore = create<I18nState>((set, get) => ({
  locale: initialLocale,
  translations: allTranslations[initialLocale],

  setLocale: (locale: Locale) => {
    if (locale === get().locale) return
    try {
      localStorage.setItem(STORAGE_KEY, locale)
    } catch { /* ignore */ }
    set({ locale, translations: allTranslations[locale] })
  },
}))

/**
 * Translation function. Supports simple interpolation: t('key', { name: 'value' })
 * Replaces {name} placeholders in the translated string.
 */
export function t(key: string, params?: Record<string, string | number>): string {
  const { translations } = useI18nStore.getState()
  let text = translations[key] ?? en[key] ?? key
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      text = text.replace(`{${k}}`, String(v))
    }
  }
  return text
}

/**
 * React hook version — triggers re-render when locale changes.
 * Returns a t() function bound to the current translations.
 */
export function useTranslation() {
  const { translations, locale } = useI18nStore()
  const tf = (key: string, params?: Record<string, string | number>): string => {
    let text = translations[key] ?? en[key] ?? key
    if (params) {
      for (const [k, v] of Object.entries(params)) {
        text = text.replace(`{${k}}`, String(v))
      }
    }
    return text
  }
  return { t: tf, locale }
}
