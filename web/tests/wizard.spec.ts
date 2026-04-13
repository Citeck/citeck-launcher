import { test, expect } from '@playwright/test'

test.describe('Namespace Creation Wizard', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/wizard')
  })

  test('shows step 1: Name', async ({ page }) => {
    await expect(page.getByText('Create Namespace')).toBeVisible()
    await expect(page.getByText('Namespace Name')).toBeVisible()
    // Default value is empty, placeholder is "my-namespace"
    const input = page.locator('input[type="text"]')
    await expect(input).toBeVisible()
  })

  test('Next button advances when name is filled', async ({ page }) => {
    await page.fill('input[type="text"]', 'TestNS')
    await page.click('button:has-text("Next")')
    await expect(page.getByText('Authentication Type')).toBeVisible()
  })

  test('step navigation: Back returns to previous step', async ({ page }) => {
    await page.fill('input[type="text"]', 'TestNS')
    await page.click('button:has-text("Next")')
    await expect(page.getByText('Authentication Type')).toBeVisible()

    await page.click('button:has-text("Back")')
    await expect(page.getByText('Namespace Name')).toBeVisible()
  })

  test('full wizard flow to Review step', async ({ page }) => {
    // Step 1: Name
    await page.fill('input[type="text"]', 'My Production')
    await page.click('button:has-text("Next")')

    // Step 2: Auth (BASIC default)
    await expect(page.getByText('Authentication Type')).toBeVisible()
    await page.click('button:has-text("Next")')

    // Step 3: Users
    await expect(page.locator('label:has-text("Users")')).toBeVisible()
    await page.fill('input[type="text"]', 'admin')
    await page.click('button:has-text("Next")')

    // Step 4: Hostname
    await expect(page.locator('label:has-text("Hostname")')).toBeVisible()
    await page.click('button:has-text("Next")')

    // Step 5: TLS
    await expect(page.getByText('TLS Configuration')).toBeVisible()
    await page.click('button:has-text("Next")')

    // Step 6: Port
    await expect(page.locator('input[type="number"]')).toBeVisible()
    await page.click('button:has-text("Next")')

    // Step 7: PgAdmin
    await expect(page.locator('input[type="checkbox"]')).toBeVisible()
    await page.click('button:has-text("Next")')

    // Step 8: Review
    await expect(page.locator('label:has-text("Review")')).toBeVisible()
    await expect(page.getByText('My Production')).toBeVisible()
  })

  test('KEYCLOAK auth skips Users step', async ({ page }) => {
    await page.fill('input[type="text"]', 'Test')
    await page.click('button:has-text("Next")')

    // Step 2: select KEYCLOAK
    await page.selectOption('select', 'KEYCLOAK')
    await page.click('button:has-text("Next")')

    // Should skip to Hostname, not Users
    await expect(page.locator('label:has-text("Hostname")')).toBeVisible()
  })

  test('TLS selection updates default port', async ({ page }) => {
    await page.fill('input[type="text"]', 'Test')
    // Navigate to TLS step
    await page.click('button:has-text("Next")') // → Auth
    await page.click('button:has-text("Next")') // → Users
    await page.click('button:has-text("Next")') // → Hostname
    await page.click('button:has-text("Next")') // → TLS

    // Select self-signed TLS
    await page.selectOption('select', 'self-signed')
    await page.click('button:has-text("Next")') // → Port

    // Port should default to 443
    const portInput = page.locator('input[type="number"]')
    await expect(portInput).toHaveValue('443')
  })
})
