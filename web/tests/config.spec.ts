import { test, expect } from '@playwright/test'

test.describe('Config Page', () => {
  // /config is workspace-level (no namespace guard) and the Settings gear is
  // always in the top bar, so this works with or without an active namespace.
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    const sidebar = page.locator('aside')
    const welcome = page.getByText('Welcome To Citeck Launcher!')
    await expect(sidebar.or(welcome)).toBeVisible({ timeout: 15_000 })
    await page.getByRole('button', { name: 'Settings', exact: true }).click()
    await expect(page).toHaveURL('/config')
  })

  test('shows the Settings heading', async ({ page }) => {
    await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible()
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
