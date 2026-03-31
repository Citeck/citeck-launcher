import '@testing-library/jest-dom'
import en from '../locales/en'
import { useI18nStore } from '../lib/i18n'

// Ensure English locale for tests
useI18nStore.setState({ locale: 'en', translations: en })
