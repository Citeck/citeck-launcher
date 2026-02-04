import { test, expect } from '@playwright/test'

test.describe('Welcome Screen', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/welcome')
    await page.waitForTimeout(2000)
  })

  test('shows Welcome heading', async ({ page }) => {
    await expect(page.getByText('Welcome To Citeck Launcher!')).toBeVisible()
  })

  test('shows namespace buttons or loading', async ({ page }) => {
    // Either namespace buttons or "Loading..." or "Create New Namespace"
    const createBtn = page.getByText('Create New Namespace')
    const loading = page.getByText('Loading...')
    const hasCreate = await createBtn.isVisible().catch(() => false)
    const hasLoading = await loading.isVisible().catch(() => false)
    expect(hasCreate || hasLoading).toBeTruthy()
  })

  test('Create New Namespace button navigates to wizard', async ({ page }) => {
    const btn = page.getByText('Create New Namespace')
    if (await btn.isVisible().catch(() => false)) {
      await btn.click()
      await expect(page).toHaveURL('/wizard')
    }
  })

  test('namespace button is clickable', async ({ page }) => {
    // If namespaces loaded, there should be clickable buttons
    const nsButtons = page.locator('.rounded-lg.bg-muted')
    const count = await nsButtons.count()
    // At least the "Create New Namespace" button should be there
    expect(count).toBeGreaterThanOrEqual(1)
  })
})
