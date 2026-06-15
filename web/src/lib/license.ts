import type { LicenseStatusDto } from './api'

/**
 * Visual severity of the dashboard license indicator:
 *   ok       — valid license, comfortably far from expiry (default styling)
 *   expiring — valid but inside the server-computed expiring-soon window (amber)
 *   expired  — license records exist but none validates (red/grey)
 */
export type LicenseBadgeVariant = 'ok' | 'expiring' | 'expired'

export function licenseBadgeVariant(
  st: Pick<LicenseStatusDto, 'enterprise' | 'expiringSoon'>,
): LicenseBadgeVariant {
  if (!st.enterprise) return 'expired'
  return st.expiringSoon ? 'expiring' : 'ok'
}

/**
 * Whether the dashboard should show the license indicator at all.
 * Community installs (no license records → no tenant) show nothing — the
 * indicator only ever surfaces enterprise context.
 */
export function showLicenseIndicator(st: LicenseStatusDto | null | undefined): st is LicenseStatusDto {
  return !!st && !!st.tenant
}
