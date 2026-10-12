import { describe, it, expect } from 'vitest'
import {
  contentStorageProblemText,
  contentStorageTypeOf,
  guardedProviderApp,
  s3OptionLabel,
  s3UnavailableText,
} from './contentStorage'
import en from '../locales/en'
import ru from '../locales/ru'

// A translator over one real locale file: the sentences are the point, so
// stubbing the lookup would test nothing.
function translatorFor(table: Record<string, string>) {
  return ((key: string, params?: Record<string, string | number>) => {
    let text = table[key] ?? key
    for (const [k, v] of Object.entries(params ?? {})) text = text.replaceAll(`{${k}}`, String(v))
    return text
  }) as never
}
const enT = translatorFor(en)
const ruT = translatorFor(ru)

describe('contentStorageTypeOf', () => {
  // Absent is DB — that is every namespace that predates the option — and the
  // daemon accepts any case, so the UI must not read "s3" as DB.
  it('reads absent as DB and normalizes the case', () => {
    expect(contentStorageTypeOf(undefined)).toBe('DB')
    expect(contentStorageTypeOf('')).toBe('DB')
    expect(contentStorageTypeOf('db')).toBe('DB')
    expect(contentStorageTypeOf('s3')).toBe('S3')
    expect(contentStorageTypeOf(' S3 ')).toBe('S3')
  })

  it('never reads an unknown value as S3 — the irreversible direction', () => {
    expect(contentStorageTypeOf('MINIO')).toBe('DB')
  })
})

describe('s3OptionLabel', () => {
  // The product name is the workspace's (contentStorage.s3.title): the UI has
  // no table of providers, so "S3 (SeaweedFS)" is exactly what the
  // configuration says — the label the CLI prints too.
  it('names the provider by the title the workspace gives it', () => {
    expect(s3OptionLabel('SeaweedFS', 'seaweedfs')).toBe('S3 (SeaweedFS)')
  })

  it('falls back to the app id, and to plain S3 while no provider is known', () => {
    expect(s3OptionLabel(undefined, 'seaweedfs')).toBe('S3 (seaweedfs)')
    expect(s3OptionLabel(' ', 'minio')).toBe('S3 (minio)')
    expect(s3OptionLabel('', '')).toBe('S3')
    expect(s3OptionLabel(undefined, undefined)).toBe('S3')
  })
})

describe('s3UnavailableText', () => {
  it('says which part of the configuration is missing, naming the provider app', () => {
    expect(s3UnavailableText('NO_PROVIDER_CONFIG', '', enT)).toContain('contentStorage.s3')
    expect(s3UnavailableText('NO_PROVIDER_APP', 'seaweedfs', enT)).toContain('seaweedfs')
    expect(s3UnavailableText('NO_PROVIDER_IMAGE', 'seaweedfs', enT)).toContain('image for seaweedfs')
    expect(s3UnavailableText('NO_CONTENT_APP', 'seaweedfs', enT)).toContain('Citeck Enterprise')
    expect(s3UnavailableText('PROVIDER_CONFLICT', 'seaweedfs', enT)).toContain('seaweedfs cannot serve S3')
  })

  // The one reason the user can fix from the dialog itself.
  it('points an unresolved bundle at the refresh button', () => {
    expect(s3UnavailableText('BUNDLE_UNRESOLVED', '', enT)).toContain('↻')
  })

  it('words the reason in the reader’s locale', () => {
    expect(s3UnavailableText('NO_PROVIDER_APP', 'seaweedfs', ruT))
      .toBe('В конфигурации рабочей области нет приложения seaweedfs, которое обслуживало бы S3.')
  })

  it('still says something for a reason this UI does not know', () => {
    expect(s3UnavailableText('FROM_A_NEWER_DAEMON', '', enT)).toBe(en['contentStorage.unavailable.unknown'])
  })
})

describe('contentStorageProblemText', () => {
  it('explains a provider the generation had to leave out', () => {
    const text = contentStorageProblemText('PROVIDER_NOT_GENERATED', 'seaweedfs', enT)
    expect(text).toContain('seaweedfs')
    expect(text).toContain('unlock the secrets')
  })

  it('keeps an unknown code visible rather than dropping it', () => {
    expect(contentStorageProblemText('SOMETHING_NEW', 'seaweedfs', enT)).toContain('SOMETHING_NEW')
  })
})

describe('guardedProviderApp', () => {
  it('is the provider of an S3 namespace and nothing for any other', () => {
    expect(guardedProviderApp({ type: 'S3', providerApp: 'seaweedfs', state: 'connected' })).toBe('seaweedfs')
    expect(guardedProviderApp(undefined)).toBe('')
    expect(guardedProviderApp({ type: 'DB', providerApp: 'seaweedfs', state: '' })).toBe('')
    expect(guardedProviderApp({ type: 'S3', providerApp: '', state: 'pending' })).toBe('')
  })
})
