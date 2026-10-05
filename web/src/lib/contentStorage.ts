import { create } from 'zustand'
import type { LocaleKey } from './i18n'
import type { ContentStorageDto } from './types'

// Content storage: where ecos-content keeps uploaded files — the database
// (DB, every namespace that predates the option) or an S3 bucket served by a
// workspace-declared provider app (S3, Enterprise bundles only). The daemon
// owns every rule (internal/namespace/content_storage.go, the write gate in
// internal/daemon/content_storage.go); this module only words what it reports.
//
// The codes below are Go constants (namespace.ContentStorage* and
// api.ContentStorage*). TypeScript cannot import them, so they are spelled out
// here; change them there and here together.

export const CONTENT_STORAGE_DB = 'DB'
export const CONTENT_STORAGE_S3 = 'S3'
export type ContentStorageType = typeof CONTENT_STORAGE_DB | typeof CONTENT_STORAGE_S3

/** The bundle has no ecos-content: S3 is not a question for it, so the option is HIDDEN. */
export const REASON_NO_CONTENT_APP = 'NO_CONTENT_APP'
export const REASON_NO_PROVIDER_CONFIG = 'NO_PROVIDER_CONFIG'
export const REASON_NO_PROVIDER_APP = 'NO_PROVIDER_APP'
export const REASON_NO_PROVIDER_IMAGE = 'NO_PROVIDER_IMAGE'
/** Generation only: the provider was left out (unreadable secret, pruned dependency). */
export const REASON_PROVIDER_NOT_GENERATED = 'PROVIDER_NOT_GENERATED'
/** Options/write gate only: the bundle is not in the synced repo, so nothing can be said. */
export const REASON_BUNDLE_UNRESOLVED = 'BUNDLE_UNRESOLVED'

// api.ContentStorageState* — the connection states NamespaceDto.contentStorage carries.
export const STATE_PENDING = 'pending'
export const STATE_WAITING = 'waiting'
export const STATE_PROVISIONING = 'provisioning'
export const STATE_CONNECTED = 'connected'
export const STATE_ADMIN_MANAGED = 'admin-managed'
export const STATE_ERROR = 'error'

type Translate = (key: LocaleKey, params?: Record<string, string | number>) => string

/** Normalizes a wire value ("DB" | "S3", any case, possibly absent) — absent is DB. */
export function contentStorageTypeOf(value: string | undefined | null): ContentStorageType {
  return (value ?? '').trim().toUpperCase() === CONTENT_STORAGE_S3 ? CONTENT_STORAGE_S3 : CONTENT_STORAGE_DB
}

// Product names for the provider apps a workspace is known to declare, for the
// option LABEL only ("S3 (SeaweedFS)", the wording the owner chose). Which app
// serves S3 is configuration (`contentStorage.s3.app`), so an app missing here
// is not an error: its id is shown as is. Sentences name the app by its id —
// that is the row the operator sees in the app table.
const PROVIDER_PRODUCT_NAMES: Record<string, string> = {
  seaweedfs: 'SeaweedFS',
}

/** "S3 (SeaweedFS)" — or just "S3" while the provider is not known. */
export function s3OptionLabel(providerApp: string | undefined): string {
  const app = (providerApp ?? '').trim()
  if (!app) return 'S3'
  return `S3 (${PROVIDER_PRODUCT_NAMES[app.toLowerCase()] ?? app})`
}

/**
 * Why S3 cannot be chosen for the bundle the dialog has selected, as one
 * sentence for the hint under the disabled option. NO_CONTENT_APP has a
 * sentence too: the create dialog hides the option for it, but an S3
 * namespace being moved to such a bundle has to be told.
 */
export function s3UnavailableText(reason: string | undefined, providerApp: string | undefined, t: Translate): string {
  const app = providerApp || 'S3'
  switch (reason) {
    case REASON_NO_CONTENT_APP:
      return t('contentStorage.unavailable.noContentApp')
    case REASON_NO_PROVIDER_CONFIG:
      return t('contentStorage.reason.noProviderConfig')
    case REASON_NO_PROVIDER_APP:
      return t('contentStorage.reason.noProviderApp', { app })
    case REASON_NO_PROVIDER_IMAGE:
      return t('contentStorage.reason.noProviderImage', { app })
    case REASON_BUNDLE_UNRESOLVED:
      return t('contentStorage.unavailable.bundleUnresolved')
    default:
      return t('contentStorage.unavailable.unknown')
  }
}

/**
 * Why the namespace's generation could not wire ecos-content to its provider
 * (NamespaceDto.contentStorage.problem). Unlike the dialog's reasons these
 * describe a namespace that ALREADY stores its content in S3.
 */
export function contentStorageProblemText(problem: string, providerApp: string | undefined, t: Translate): string {
  const app = providerApp || 'S3'
  switch (problem) {
    case REASON_NO_CONTENT_APP:
      return t('contentStorage.problem.noContentApp')
    case REASON_NO_PROVIDER_CONFIG:
      return t('contentStorage.reason.noProviderConfig')
    case REASON_NO_PROVIDER_APP:
      return t('contentStorage.reason.noProviderApp', { app })
    case REASON_NO_PROVIDER_IMAGE:
      return t('contentStorage.reason.noProviderImage', { app })
    case REASON_PROVIDER_NOT_GENERATED:
      return t('contentStorage.problem.providerNotGenerated', { app })
    default:
      // A code from a newer daemon: still say something, and keep the code —
      // it is what a support request can search for.
      return t('contentStorage.problem.unknown', { reason: problem })
  }
}

/** The connection states that are progress, not a verdict: shown as information. */
export function isContentStorageInProgress(state: string | undefined): boolean {
  return state === STATE_PENDING || state === STATE_WAITING || state === STATE_PROVISIONING
}

/**
 * The provider app the operator should be asked about before stopping it, or
 * "" — only an S3 namespace has one. Stopping it is never refused (owner
 * ruling: warn, do not block); the table asks for a confirmation instead.
 */
export function guardedProviderApp(cs: ContentStorageDto | undefined | null): string {
  if (!cs || contentStorageTypeOf(cs.type) !== CONTENT_STORAGE_S3) return ''
  return (cs.providerApp ?? '').trim()
}

interface ContentStorageBannerState {
  /** Namespace whose IN-PROGRESS banner the user hid for this session. The
   *  error and problem variants are never hidden — they describe something
   *  broken right now (the BundleErrorBanner rule). */
  dismissedNs: string | null
  dismissProgress: (nsId: string) => void
}

/** Session-scoped dismissal of the in-progress content storage banner. */
export const useContentStorageBannerStore = create<ContentStorageBannerState>((set) => ({
  dismissedNs: null,
  dismissProgress: (nsId) => set({ dismissedNs: nsId }),
}))
