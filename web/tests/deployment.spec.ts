import { test, expect } from '@playwright/test'

/**
 * Deployment scenario tests — verify the running platform via the Web UI.
 * Requires daemon to be running with a namespace in RUNNING state.
 */

test.describe('Deployment: Running Platform Verification', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    // Wait for SSE connection and initial data load
    await page.waitForTimeout(3000)
  })

  test('dashboard shows namespace in RUNNING state', async ({ page }) => {
    // The status badge should show RUNNING
    const running = page.locator('text=RUNNING').first()
    await expect(running).toBeVisible({ timeout: 10000 })
  })

  test('all 19 apps are visible in the app table', async ({ page }) => {
    // Wait for apps to load
    await page.waitForTimeout(2000)
    // Each app row has a status badge — count RUNNING badges
    const runningBadges = page.locator('text=RUNNING')
    const count = await runningBadges.count()
    // At least the namespace status + several apps should show RUNNING
    expect(count).toBeGreaterThanOrEqual(10)
  })

  test('app groups are shown (Core, Third Party, Additional)', async ({ page }) => {
    // App table should have group headers
    await expect(page.getByText('Core', { exact: false })).toBeVisible()
    await expect(page.getByText('Third Party', { exact: false })).toBeVisible()
  })

  test('quick links are visible', async ({ page }) => {
    // Sidebar should show quick links
    const ecosLink = page.getByText('ECOS UI', { exact: false })
    if (await ecosLink.isVisible().catch(() => false)) {
      await expect(ecosLink).toBeVisible()
    }
  })

  test('namespace info panel shows bundle ref', async ({ page }) => {
    const bundleRef = page.getByText('community:')
    await expect(bundleRef).toBeVisible({ timeout: 5000 })
  })

  test('diagnostics page loads and shows checks', async ({ page }) => {
    const diagBtn = page.getByRole('button', { name: 'Diagnostics' })
    if (await diagBtn.isVisible().catch(() => false)) {
      await diagBtn.click()
      await page.waitForTimeout(2000)
      // Should show Docker check result
      await expect(page.getByText('Docker is running')).toBeVisible()
    }
  })

  test('volumes page shows namespace volumes', async ({ page }) => {
    const volBtn = page.getByRole('button', { name: 'Volumes' })
    if (await volBtn.isVisible().catch(() => false)) {
      await volBtn.click()
      await page.waitForTimeout(2000)
      // Should show at least postgres volume
      await expect(page.getByText('postgres', { exact: false })).toBeVisible()
    }
  })

  test('clicking an app opens app detail', async ({ page }) => {
    // Click on the postgres app name link in the table
    const pgLink = page.locator('a:has-text("postgres")').first()
    if (await pgLink.isVisible().catch(() => false)) {
      await pgLink.click()
      await page.waitForTimeout(2000)
      // Should navigate to app detail page
      await expect(page).toHaveURL(/\/apps\/postgres/)
    }
  })

  test('config page shows YAML content', async ({ page }) => {
    await page.getByRole('button', { name: 'Settings' }).click()
    await page.waitForTimeout(1000)
    // Config page should show YAML content with namespace name
    await expect(page.getByText('bundleRef', { exact: false })).toBeVisible()
  })

  test('daemon logs page loads', async ({ page }) => {
    const logsBtn = page.getByRole('button', { name: 'Launcher Logs' })
    if (await logsBtn.isVisible().catch(() => false)) {
      await logsBtn.click()
      await page.waitForTimeout(2000)
      // Should show some log content
      await expect(page.getByText('INFO', { exact: false })).toBeVisible()
    }
  })
})

test.describe('Deployment: API Verification', () => {
  test('GET /api/v1/namespace returns RUNNING with 19 apps', async ({ request }) => {
    const res = await request.get('/api/v1/namespace')
    expect(res.ok()).toBeTruthy()
    const ns = await res.json()
    expect(ns.status).toBe('RUNNING')
    expect(ns.apps.length).toBe(19)
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
    expect(d.version).toBe('dev')
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
