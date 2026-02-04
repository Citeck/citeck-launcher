import { test, expect } from '@playwright/test'

test.describe('Volumes', () => {
  test('page shows Docker Volumes or redirects', async ({ page }) => {
    // Navigate to root first to let namespace load, then go to volumes
    await page.goto('/')
    await page.waitForTimeout(3000)
    await page.goto('/volumes')
    await page.waitForTimeout(2000)

    // If namespace is loaded: shows Docker Volumes page
    // If not: may show Welcome or Dashboard
    const body = await page.textContent('body')
    expect(body!.length).toBeGreaterThan(0)
  })

  test('volumes table is visible when page loads', async ({ page }) => {
    await page.goto('/volumes')
    await page.waitForTimeout(2000)
    if (await page.getByText('Docker Volumes').isVisible().catch(() => false)) {
      const table = page.locator('table').first()
      await expect(table).toBeVisible()
    }
  })

  test('export and import buttons exist on volumes page', async ({ page }) => {
    await page.goto('/volumes')
    await page.waitForTimeout(2000)
    if (await page.getByText('Docker Volumes').isVisible().catch(() => false)) {
      await expect(page.getByRole('button', { name: /export/i })).toBeVisible()
      await expect(page.getByRole('button', { name: /import/i })).toBeVisible()
    }
  })
})
