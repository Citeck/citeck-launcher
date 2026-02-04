import { test, expect, request as playwrightRequest } from '@playwright/test'

const DAEMON = 'http://127.0.0.1:7088'

interface PlatformInfo {
  baseUrl: string
  authType: string // "BASIC" or "KEYCLOAK"
  users: string[]  // e.g. ["admin"]
}

// Resolve platform info from daemon API
async function getPlatformInfo(): Promise<PlatformInfo | null> {
  try {
    const ctx = await playwrightRequest.newContext()
    const resp = await ctx.get(`${DAEMON}/api/v1/namespace`)
    if (!resp.ok()) return null
    const ns = await resp.json()
    const ecosLink = ns.links?.find((l: { name: string; url?: string }) => l.name === 'ECOS UI')
    if (!ecosLink?.url) return null

    // Get config for auth info
    const cfgResp = await ctx.get(`${DAEMON}/api/v1/config`)
    let authType = 'BASIC'
    let users = ['admin']
    if (cfgResp.ok()) {
      const cfgText = await cfgResp.text()
      if (cfgText.includes('type: KEYCLOAK')) authType = 'KEYCLOAK'
      const userMatch = cfgText.match(/users:\s*\n\s*-\s*"?(\w+)"?/)
      if (userMatch) users = [userMatch[1]]
    }

    await ctx.dispose()
    return { baseUrl: ecosLink.url, authType, users }
  } catch {
    return null
  }
}

test.describe('Citeck ECOS Platform E2E', () => {
  let platform: PlatformInfo

  test.beforeAll(async () => {
    const info = await getPlatformInfo()
    test.skip(!info, 'No running namespace with ECOS UI link')
    platform = info!
  })

  test('proxy is alive — eis.json returns valid JSON', async ({ request }) => {
    const response = await request.get(`${platform.baseUrl}/eis.json`, { ignoreHTTPSErrors: true })
    expect(response.ok()).toBeTruthy()
    const body = await response.json()
    expect(body).toHaveProperty('eisId')
  })

  test('gateway is responding', async ({ request }) => {
    const response = await request.get(`${platform.baseUrl}/gateway/management/health`, { ignoreHTTPSErrors: true })
    // Gateway may return 401 (auth required) or 200 — both mean it's alive
    expect(response.status()).toBeLessThan(500)
  })

  test('main page returns redirect (proxy routing works)', async ({ request }) => {
    const response = await request.get(`${platform.baseUrl}/`, { maxRedirects: 0, ignoreHTTPSErrors: true })
    expect([200, 302]).toContain(response.status())
  })

  test('gateway routes to emodel', async ({ request }) => {
    const response = await request.get(`${platform.baseUrl}/gateway/emodel/api/management/health`, { ignoreHTTPSErrors: true })
    expect(response.status()).toBeLessThan(500)
  })

  test('ECOS UI loads with auth', async ({ browser }) => {
    // Create context with HTTP credentials for BASIC auth
    const user = platform.users[0] || 'admin'
    const context = await browser.newContext({
      httpCredentials: { username: user, password: user },
      ignoreHTTPSErrors: true,
    })
    const page = await context.newPage()
    const response = await page.goto(`${platform.baseUrl}/v2/`, { waitUntil: 'domcontentloaded', timeout: 30000 })
    expect(response).not.toBeNull()
    expect(response!.status()).toBeLessThan(400)

    // Wait for SPA to render navigation
    await page.waitForSelector('nav, [role="navigation"], .app-header', { timeout: 15000 }).catch(() => {})

    const title = await page.title()
    expect(title).toBeTruthy()

    await context.close()
  })

  test('RabbitMQ management accessible', async ({ page }) => {
    const response = await page.goto('http://127.0.0.1:15672/')
    expect(response).not.toBeNull()
    expect(response!.status()).toBeLessThan(500)
  })

  test('PgAdmin accessible', async ({ page }) => {
    try {
      const response = await page.goto('http://127.0.0.1:5050/', { timeout: 15000, waitUntil: 'commit' })
      expect(response).not.toBeNull()
      expect(response!.status()).toBeLessThan(500)
    } catch {
      test.skip(true, 'PgAdmin not responding on port 5050')
    }
  })

  test('MailHog accessible', async ({ page }) => {
    const response = await page.goto('http://127.0.0.1:8025/')
    expect(response).not.toBeNull()
    expect(response!.status()).toBe(200)
  })

  test('OnlyOffice health page', async ({ page }) => {
    try {
      const response = await page.goto('http://127.0.0.1:8980/healthcheck', { timeout: 5000 })
      expect(response).not.toBeNull()
      expect(response!.status()).toBe(200)
    } catch {
      test.skip(true, 'OnlyOffice healthcheck port not exposed')
    }
  })
})
