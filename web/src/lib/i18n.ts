import { create } from 'zustand'
import enDefault from '../locales/en'

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

// English is bundled synchronously — always available on first render
const enTranslations: Translations = enDefault

// Lazy-loaded locale modules (non-English)
const loaders: Record<Locale, () => Promise<{ default: Translations }>> = {
  en: () => Promise.resolve({ default: enTranslations }),
  ru: () => import('../locales/ru'),
  zh: () => import('../locales/zh'),
  es: () => import('../locales/es'),
  de: () => import('../locales/de'),
  fr: () => import('../locales/fr'),
  pt: () => import('../locales/pt'),
  ja: () => import('../locales/ja'),
}

function detectLocale(): Locale {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored && stored in loaders) return stored as Locale
  } catch { /* ignore */ }
  const lang = navigator.language.split('-')[0]
  if (lang in loaders) return lang as Locale
  return 'en'
}

interface I18nState {
  locale: Locale
  translations: Translations
  loading: boolean
  setLocale: (locale: Locale) => void
}

const initialLocale = detectLocale()

export const useI18nStore = create<I18nState>((set, get) => ({
  // Start with English translations immediately — no flash of raw keys
  locale: initialLocale === 'en' ? 'en' : 'en',
  translations: enTranslations,
  loading: initialLocale !== 'en',

  setLocale: async (locale: Locale) => {
    try {
      localStorage.setItem(STORAGE_KEY, locale)
    } catch { /* ignore */ }
    if (locale === get().locale && !get().loading) return
    set({ loading: true })
    try {
      const mod = await loaders[locale]()
      set({ locale, translations: mod.default, loading: false })
    } catch {
      // Fallback to English
      set({ locale: 'en', translations: enTranslations, loading: false })
    }
  },
}))

// Load non-English locale if detected
if (initialLocale !== 'en') {
  loaders[initialLocale]().then(mod => {
    useI18nStore.setState({ locale: initialLocale, translations: mod.default, loading: false })
  }).catch(() => {
    useI18nStore.setState({ loading: false })
  })
}

/**
 * Translation function. Supports simple interpolation: t('key', { name: 'value' })
 * Replaces {name} placeholders in the translated string.
 */
export function t(key: string, params?: Record<string, string | number>): string {
  const { translations } = useI18nStore.getState()
  let text = translations[key] ?? enTranslations[key] ?? key
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
    let text = translations[key] ?? enTranslations[key] ?? key
    if (params) {
      for (const [k, v] of Object.entries(params)) {
        text = text.replace(`{${k}}`, String(v))
      }
    }
    return text
  }
  return { t: tf, locale }
}
