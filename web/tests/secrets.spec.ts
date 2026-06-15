import { test, expect } from '@playwright/test'

/**
 * /secrets renders the shared SecretsDialog (the same component the
 * Dashboard sidebar opens) — workspace-level, so no namespace gating.
 */
test.describe('Secrets Management', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/secrets')
    await expect(page.getByRole('heading', { name: 'Auth Secrets' })).toBeVisible({ timeout: 15_000 })
  })

  test('renders the Auth Secrets dialog with table columns', async ({ page }) => {
    const dialog = page.locator('dialog[open]')
    await expect(dialog.locator('th', { hasText: 'Name' })).toBeVisible()
    await expect(dialog.locator('th', { hasText: 'Type' })).toBeVisible()
    await expect(dialog.locator('th', { hasText: 'Scope' })).toBeVisible()
  })

  test('Create button opens the secret form with scope picker', async ({ page }) => {
    await page.getByRole('button', { name: 'Create' }).click()
    await expect(page.getByRole('heading', { name: 'Add Secret' })).toBeVisible()
    // Form inputs, identified by their placeholders (labels carry a '*' for
    // required fields, so placeholder matching is the stable hook).
    await expect(page.getByPlaceholder('unique-id')).toBeVisible()
    await expect(page.getByPlaceholder('My Secret')).toBeVisible()
    await expect(page.getByPlaceholder('secret value')).toBeVisible()
    // The scope picker binds the secret to a repo / registry on the daemon.
    // It defaults to Global; the free-text input appears via "Custom…".
    await page.getByRole('button', { name: 'Global (default)' }).click()
    await page.getByRole('option', { name: 'Custom…' }).click()
    await expect(page.getByPlaceholder('images-repo:registry.example.com')).toBeVisible()
  })
})
