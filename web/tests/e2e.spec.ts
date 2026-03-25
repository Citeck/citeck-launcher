import { test, expect } from '@playwright/test'

const BASE = 'https://custom.launcher.ru:8443'

test.describe('Citeck ECOS Platform E2E', () => {

  test('proxy is alive — eis.json returns valid JSON', async ({ request }) => {
    const response = await request.get(`${BASE}/eis.json`)
    expect(response.ok()).toBeTruthy()
    const body = await response.json()
    expect(body).toHaveProperty('eisId')
  })

  test('gateway health check returns UP', async ({ request }) => {
    const response = await request.get(`${BASE}/gateway/management/health`)
    expect(response.ok()).toBeTruthy()
    const body = await response.json()
    expect(body.status).toBe('UP')
  })

  test('main page returns redirect (proxy routing works)', async ({ request }) => {
    const response = await request.get(`${BASE}/`, { maxRedirects: 0 })
    // Proxy redirects to /v2/ — 302 is expected
    expect([200, 302]).toContain(response.status())
  })

  test('gateway routes to emodel', async ({ request }) => {
    const response = await request.get(`${BASE}/gateway/emodel/api/management/health`)
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
    const response = await page.goto('http://127.0.0.1:8980/healthcheck')
    expect(response).not.toBeNull()
    expect(response!.status()).toBe(200)
  })
})
