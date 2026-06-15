import { test, expect } from '@playwright/test'

test.describe('Config Page', () => {
  // /config is namespace-gated (redirects to root without one), so reach it
  // through the Settings gear after the root screen settles.
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    const sidebar = page.locator('aside')
    const welcome = page.getByText('Welcome To Citeck Launcher!')
    await expect(sidebar.or(welcome)).toBeVisible({ timeout: 15_000 })
    test.skip(await welcome.isVisible(), 'no active namespace — /config redirects to root')
    await page.getByRole('button', { name: 'Settings' }).click()
    await expect(page).toHaveURL('/config')
  })

  test('shows the Configuration heading', async ({ page }) => {
    await expect(page.getByRole('heading', { name: 'Configuration' })).toBeVisible()
  })

  test('shows the system health section', async ({ page }) => {
    await expect(page.getByRole('heading', { name: 'System Health' })).toBeVisible({ timeout: 15_000 })
  })
})

test.describe('Daemon Logs', () => {
  test('page loads the log viewer', async ({ page }) => {
    await page.goto('/daemon-logs')
    // The LogViewer toolbar is always rendered (search box + Wrap toggle).
    await expect(page.getByPlaceholder('Search... (Ctrl+F)')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByRole('button', { name: 'Wrap' })).toBeVisible()
  })
})
