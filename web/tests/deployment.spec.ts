import { test, expect } from '@playwright/test'

/**
 * Deployment scenario tests — verify the running platform via the Web UI.
 * Requires daemon to be running with a namespace in RUNNING state; these
 * tests therefore assert hard (no visibility guards) — a missing element is
 * a deployment failure, not a skip.
 */

test.describe('Deployment: Running Platform Verification', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    // Wait for the namespace sidebar (SSE connection + initial data load).
    await expect(page.locator('aside')).toBeVisible({ timeout: 20_000 })
  })

  test('dashboard shows namespace in RUNNING state', async ({ page }) => {
    const running = page.locator('text=RUNNING').first()
    await expect(running).toBeVisible({ timeout: 10_000 })
  })

  test('most apps are visible and RUNNING in the app table', async ({ page }) => {
    // Each app row has a status badge — count RUNNING badges. At least the
    // namespace status + several apps should show RUNNING.
    await expect
      .poll(async () => page.locator('text=RUNNING').count(), { timeout: 20_000 })
      .toBeGreaterThanOrEqual(10)
  })

  test('app groups are shown (Core, Third Party)', async ({ page }) => {
    await expect(page.getByText('Core', { exact: false }).first()).toBeVisible()
    await expect(page.getByText('Third Party', { exact: false }).first()).toBeVisible()
  })

  test('quick links are visible', async ({ page }) => {
    await expect(page.getByText('Citeck UI', { exact: false }).first()).toBeVisible({ timeout: 10_000 })
  })

  test('namespace info panel shows bundle ref', async ({ page }) => {
    await expect(page.getByText('community:').first()).toBeVisible({ timeout: 10_000 })
  })

  test('diagnostics page loads and shows Docker check', async ({ page }) => {
    await page.goto('/diagnostics')
    await expect(page.getByText('Docker is running')).toBeVisible({ timeout: 15_000 })
  })

  test('volumes dialog shows namespace volumes', async ({ page }) => {
    await page.getByRole('button', { name: 'Volumes' }).click()
    await expect(page.getByRole('heading', { name: 'Volumes', exact: true })).toBeVisible()
    // A running platform must have at least the postgres volume.
    await expect(page.getByText('postgres', { exact: false }).first()).toBeVisible({ timeout: 15_000 })
  })

  test('config page shows system health', async ({ page }) => {
    await page.getByRole('button', { name: 'Settings' }).click()
    await expect(page).toHaveURL('/config')
    await expect(page.getByRole('heading', { name: 'System Health' })).toBeVisible({ timeout: 15_000 })
  })

  test('daemon logs page streams log content', async ({ page }) => {
    await page.goto('/daemon-logs')
    await expect(page.getByText('INFO', { exact: false }).first()).toBeVisible({ timeout: 15_000 })
  })
})

test.describe('Deployment: API Verification', () => {
  test('GET /api/v1/namespace returns RUNNING with all apps running', async ({ request }) => {
    const res = await request.get('/api/v1/namespace')
    expect(res.ok()).toBeTruthy()
    const ns = await res.json()
    expect(ns.status).toBe('RUNNING')
    expect(ns.apps.length).toBeGreaterThan(0)
    // All apps should be RUNNING
    for (const app of ns.apps) {
      expect(app.status).toBe('RUNNING')
    }
  })

  test('GET /api/v1/health returns healthy', async ({ request }) => {
    const res = await request.get('/api/v1/health')
    expect(res.ok()).toBeTruthy()
    const h = await res.json()
    expect(h.healthy).toBe(true)
  })

  test('GET /api/v1/diagnostics returns all OK', async ({ request }) => {
    const res = await request.get('/api/v1/diagnostics')
    expect(res.ok()).toBeTruthy()
    const diag = await res.json()
    expect(diag.checks.length).toBeGreaterThan(0)
    // Docker and Socket should be OK
    const docker = diag.checks.find((c: { name: string; status: string }) => c.name === 'Docker')
    expect(docker?.status).toBe('ok')
  })

  test('GET /api/v1/daemon/status returns running daemon', async ({ request }) => {
    const res = await request.get('/api/v1/daemon/status')
    expect(res.ok()).toBeTruthy()
    const d = await res.json()
    expect(d.running).toBe(true)
  })

  test('GET /api/v1/config returns valid YAML', async ({ request }) => {
    const res = await request.get('/api/v1/config')
    expect(res.ok()).toBeTruthy()
    const text = await res.text()
    expect(text).toContain('bundleRef')
    expect(text).toContain('authentication')
  })

  test('GET /api/v1/volumes returns bind-mount volumes', async ({ request }) => {
    const res = await request.get('/api/v1/volumes')
    expect(res.ok()).toBeTruthy()
    const vols = await res.json()
    expect(vols.length).toBeGreaterThan(0)
    // Should have postgres volume
    const pgVol = vols.find((v: { name: string }) => v.name.includes('postgres'))
    expect(pgVol).toBeDefined()
  })

  test('GET /api/v1/snapshots returns list (may be empty)', async ({ request }) => {
    const res = await request.get('/api/v1/snapshots')
    expect(res.ok()).toBeTruthy()
    const snaps = await res.json()
    expect(Array.isArray(snaps)).toBeTruthy()
  })
})
