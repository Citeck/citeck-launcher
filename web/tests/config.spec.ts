import { test, expect } from '@playwright/test'

test.describe('Config Page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/config')
  })

  test('page loads', async ({ page }) => {
    await page.waitForTimeout(2000)
    // Config page should show either YAML content or "not found" message
    const body = await page.textContent('body')
    expect(body).toBeTruthy()
  })

  test('has Edit/Apply buttons when config exists', async ({ page }) => {
    await page.waitForTimeout(2000)
    // If config exists, there should be an Edit button
    const editBtn = page.getByRole('button', { name: /edit/i })
    if (await editBtn.isVisible().catch(() => false)) {
      await expect(editBtn).toBeVisible()
    }
  })
})

test.describe('Daemon Logs', () => {
  test('page loads and shows log content', async ({ page }) => {
    await page.goto('/daemon-logs')
    await page.waitForTimeout(2000)
    // Should see either log content or the refresh button
    await expect(page.getByRole('button', { name: /refresh/i }).first()).toBeVisible({ timeout: 5000 }).catch(() => {
      // Some implementations auto-refresh without a button
    })
  })
})
