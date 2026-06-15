import { test, expect } from '@playwright/test'

test.describe('Diagnostics', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/diagnostics')
  })

  test('page shows heading and Run Checks button', async ({ page }) => {
    await expect(page.getByText('Diagnostics').first()).toBeVisible()
    await expect(page.getByRole('button', { name: /run checks/i })).toBeVisible()
  })

  test('checks load automatically on mount', async ({ page }) => {
    // At least one check row (Docker, Socket, Config, …) must appear.
    await expect(page.locator('tbody tr').first()).toBeVisible({ timeout: 15_000 })
    expect(await page.locator('tbody tr').count()).toBeGreaterThan(0)
  })

  test('Run Checks button refreshes the check list', async ({ page }) => {
    await expect(page.locator('tbody tr').first()).toBeVisible({ timeout: 15_000 })
    await page.click('button:has-text("Run Checks")')
    await expect(page.locator('tbody tr').first()).toBeVisible({ timeout: 15_000 })
    expect(await page.locator('tbody tr').count()).toBeGreaterThan(0)
  })

  test('check status badges show a known status', async ({ page }) => {
    await expect(page.locator('tbody tr').first()).toBeVisible({ timeout: 15_000 })
    const badges = page.locator('span.inline-block.rounded')
    await expect(badges.first()).toBeVisible()
    const text = await badges.first().textContent()
    expect(['ok', 'warn', 'warning', 'error']).toContain(text?.trim())
  })
})
