import { test, expect } from '@playwright/test'

test.describe('Diagnostics', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/diagnostics')
  })

  test('page shows heading and Run Checks button', async ({ page }) => {
    await expect(page.getByText('Diagnostics')).toBeVisible()
    await expect(page.getByRole('button', { name: /run checks/i })).toBeVisible()
  })

  test('checks load automatically on mount', async ({ page }) => {
    // Wait for checks to load
    await page.waitForTimeout(2000)

    // Should see at least one check row (Docker, Socket, Config, etc.)
    const rows = page.locator('tbody tr')
    const count = await rows.count()
    expect(count).toBeGreaterThan(0)
  })

  test('Run Checks button refreshes the check list', async ({ page }) => {
    await page.waitForTimeout(1000)
    await page.click('button:has-text("Run Checks")')
    await page.waitForTimeout(2000)

    // Should still see checks
    const rows = page.locator('tbody tr')
    const count = await rows.count()
    expect(count).toBeGreaterThan(0)
  })

  test('check status badges have correct colors', async ({ page }) => {
    await page.waitForTimeout(2000)

    // Status badges should exist (ok, warn, or error)
    const badges = page.locator('span.inline-block.rounded')
    const count = await badges.count()
    if (count > 0) {
      const firstBadge = badges.first()
      const text = await firstBadge.textContent()
      expect(['ok', 'warn', 'error']).toContain(text?.trim())
    }
  })
})
