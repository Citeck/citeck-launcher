import { describe, it, expect } from 'vitest'
import { licenseBadgeVariant, showLicenseIndicator } from './license'
import type { LicenseStatusDto } from './api'

const base: LicenseStatusDto = {
  enterprise: true,
  tenant: 'acme',
  validUntil: '2026-09-01',
  daysLeft: 83,
  expiringSoon: false,
}

describe('licenseBadgeVariant', () => {
  it('valid license far from expiry is ok', () => {
    expect(licenseBadgeVariant(base)).toBe('ok')
  })

  it('valid license inside the expiring-soon window is expiring', () => {
    const expiring: LicenseStatusDto = { ...base, daysLeft: 10, expiringSoon: true }
    expect(licenseBadgeVariant(expiring)).toBe('expiring')
  })

  it('invalid license is expired regardless of expiringSoon', () => {
    expect(licenseBadgeVariant({ ...base, enterprise: false })).toBe('expired')
    // Defensive: a malformed payload claiming expiringSoon while invalid
    // must still render as expired.
    expect(licenseBadgeVariant({ enterprise: false, expiringSoon: true } as LicenseStatusDto)).toBe('expired')
  })
})

describe('showLicenseIndicator', () => {
  it('hides for null/undefined (endpoint missing or fetch failed)', () => {
    expect(showLicenseIndicator(null)).toBe(false)
    expect(showLicenseIndicator(undefined)).toBe(false)
  })

  it('hides for community installs (no tenant)', () => {
    expect(showLicenseIndicator({ enterprise: false, daysLeft: 0, expiringSoon: false })).toBe(false)
    expect(showLicenseIndicator({ enterprise: false, tenant: '', daysLeft: 0, expiringSoon: false })).toBe(false)
  })

  it('shows whenever a tenant is known — valid or expired', () => {
    expect(showLicenseIndicator(base)).toBe(true)
    expect(showLicenseIndicator({ ...base, enterprise: false, daysLeft: -10 })).toBe(true)
  })
})
