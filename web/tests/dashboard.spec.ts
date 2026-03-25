import { test, expect } from '@playwright/test'

test.describe('Dashboard', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    await page.waitForTimeout(3000)
  })

  test('page loads with tab bar', async ({ page }) => {
    // Home tab should be visible (either "Welcome" or "Dashboard")
    const homeTab = page.locator('[class*="cursor-pointer"]').first()
    await expect(homeTab).toBeVisible()
  })

  test('sidebar shows namespace info or loading state', async ({ page }) => {
    // If namespace loaded: sidebar visible. If not: Welcome screen visible.
    const sidebar = page.locator('.w-52')
    const welcome = page.getByText('Welcome To Citeck Launcher!')
    const hasSidebar = await sidebar.isVisible().catch(() => false)
    const hasWelcome = await welcome.isVisible().catch(() => false)
    expect(hasSidebar || hasWelcome).toBeTruthy()
  })

  test('sidebar nav buttons exist when namespace is loaded', async ({ page }) => {
    // Only test if Dashboard (namespace loaded)
    const sidebar = page.locator('.w-52')
    if (await sidebar.isVisible().catch(() => false)) {
      for (const label of ['Volumes', 'Secrets', 'Diagnostics', 'Launcher Logs']) {
        await expect(page.getByRole('button', { name: label })).toBeVisible()
      }
    }
  })

  test('clicking Volumes opens Volumes page', async ({ page }) => {
    const volBtn = page.getByRole('button', { name: 'Volumes' })
    if (await volBtn.isVisible().catch(() => false)) {
      await volBtn.click()
      await expect(page).toHaveURL('/volumes')
      await expect(page.getByText('Docker Volumes')).toBeVisible()
    }
  })

  test('clicking Secrets opens Secrets page', async ({ page }) => {
    const btn = page.getByRole('button', { name: 'Secrets' })
    if (await btn.isVisible().catch(() => false)) {
      await btn.click()
      await expect(page).toHaveURL('/secrets')
    }
  })

  test('clicking Diagnostics opens Diagnostics page', async ({ page }) => {
    const btn = page.getByRole('button', { name: 'Diagnostics' })
    if (await btn.isVisible().catch(() => false)) {
      await btn.click()
      await expect(page).toHaveURL('/diagnostics')
    }
  })

  test('settings button opens config page', async ({ page }) => {
    await page.getByRole('button', { name: 'Settings' }).click()
    await expect(page).toHaveURL('/config')
  })

  test('theme toggle works', async ({ page }) => {
    const html = page.locator('html')
    const themeBtn = page.locator('button[title*="theme"]')
    await expect(themeBtn).toBeVisible()

    const before = await html.getAttribute('data-theme')
    await themeBtn.click()
    const after = await html.getAttribute('data-theme')
    expect(after).not.toBe(before)
  })
})
