import '@testing-library/jest-dom'
import en from '../locales/en'
import { useI18nStore } from '../lib/i18n'

// Pre-load English translations synchronously for tests
useI18nStore.setState({ locale: 'en', translations: en, loading: false })
