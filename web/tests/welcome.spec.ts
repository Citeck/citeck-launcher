import { test, expect } from '@playwright/test'

test.describe('Welcome Screen', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/welcome')
  })

  test('shows Welcome heading', async ({ page }) => {
    await expect(page.getByText('Welcome To Citeck Launcher!')).toBeVisible()
  })

  test('shows Create New Namespace once loading settles', async ({ page }) => {
    // The list resolves from "Loading..." into the namespace buttons; the
    // Create New Namespace button is always rendered on a successful load.
    await expect(page.getByText('Create New Namespace')).toBeVisible({ timeout: 15_000 })
  })

  test('Create New Namespace button opens the create dialog', async ({ page }) => {
    const btn = page.getByText('Create New Namespace')
    await expect(btn).toBeVisible({ timeout: 15_000 })
    await btn.click()
    await expect(page.getByRole('heading', { name: 'Create Namespace' })).toBeVisible()
  })

  test('namespace button list renders at least one button', async ({ page }) => {
    await expect(page.getByText('Create New Namespace')).toBeVisible({ timeout: 15_000 })
    // At minimum the "Create New Namespace" button itself uses this style.
    const nsButtons = page.locator('.rounded-lg.bg-muted')
    expect(await nsButtons.count()).toBeGreaterThanOrEqual(1)
  })
})
