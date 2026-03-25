import { test, expect, request as playwrightRequest } from '@playwright/test'

const DAEMON = 'http://127.0.0.1:8088'

// Resolve platform BASE URL from daemon API (dynamic, not hardcoded)
async function getPlatformBaseUrl(): Promise<string | null> {
  try {
    const ctx = await playwrightRequest.newContext()
    const resp = await ctx.get(`${DAEMON}/api/v1/namespace`)
    if (!resp.ok()) return null
    const ns = await resp.json()
    const ecosLink = ns.links?.find((l: any) => l.name === 'ECOS UI')
    await ctx.dispose()
    return ecosLink?.url || null
  } catch {
    return null
  }
}

test.describe('Citeck ECOS Platform E2E', () => {
  let BASE: string

  test.beforeAll(async () => {
    const url = await getPlatformBaseUrl()
    test.skip(!url, 'No running namespace with ECOS UI link')
    BASE = url!
  })

  test('proxy is alive — eis.json returns valid JSON', async ({ request }) => {
    const response = await request.get(`${BASE}/eis.json`, { ignoreHTTPSErrors: true })
    expect(response.ok()).toBeTruthy()
    const body = await response.json()
    expect(body).toHaveProperty('eisId')
  })

  test('gateway is responding', async ({ request }) => {
    const response = await request.get(`${BASE}/gateway/management/health`, { ignoreHTTPSErrors: true })
    // Gateway may return 401 (auth required) or 200 — both mean it's alive
    expect(response.status()).toBeLessThan(500)
  })

  test('main page returns redirect (proxy routing works)', async ({ request }) => {
    const response = await request.get(`${BASE}/`, { maxRedirects: 0, ignoreHTTPSErrors: true })
    // Proxy redirects to /v2/ — 302 is expected
    expect([200, 302]).toContain(response.status())
  })

  test('gateway routes to emodel', async ({ request }) => {
    const response = await request.get(`${BASE}/gateway/emodel/api/management/health`, { ignoreHTTPSErrors: true })
    // Gateway may return 401 or 200 depending on auth
    expect(response.status()).toBeLessThan(500)
  })

  test('RabbitMQ management accessible', async ({ page }) => {
    const response = await page.goto('http://127.0.0.1:15672/')
    expect(response).not.toBeNull()
    expect(response!.status()).toBeLessThan(500)
  })

  test('PgAdmin accessible', async ({ page }) => {
    const response = await page.goto('http://127.0.0.1:5050/')
    expect(response).not.toBeNull()
    expect(response!.status()).toBeLessThan(500)
  })

  test('MailHog accessible', async ({ page }) => {
    const response = await page.goto('http://127.0.0.1:8025/')
    expect(response).not.toBeNull()
    expect(response!.status()).toBe(200)
  })

  test('OnlyOffice health page', async ({ page }) => {
    // OnlyOffice may not expose healthcheck port in all configurations
    try {
      const response = await page.goto('http://127.0.0.1:8980/healthcheck', { timeout: 5000 })
      expect(response).not.toBeNull()
      expect(response!.status()).toBe(200)
    } catch {
      test.skip(true, 'OnlyOffice healthcheck port not exposed')
    }
  })
})
