import { test, expect, type Page } from '@playwright/test'

/**
 * Volume management lives in the VolumesDialog (opened from the Dashboard
 * sidebar; the /volumes route renders the same dialog). The dialog requires
 * an active namespace, so tests skip explicitly when Welcome is shown.
 */
async function openVolumesDialog(page: Page): Promise<boolean> {
  await page.goto('/')
  const sidebar = page.locator('aside')
  const welcome = page.getByText('Welcome To Citeck Launcher!')
  await expect(sidebar.or(welcome)).toBeVisible({ timeout: 15_000 })
  if (!(await sidebar.isVisible())) return false
  await page.getByRole('button', { name: 'Volumes' }).click()
  await expect(page.getByRole('heading', { name: 'Volumes', exact: true })).toBeVisible()
  return true
}

test.describe('Volumes dialog', () => {
  test('shows the volumes table with Name/Size columns', async ({ page }) => {
    const opened = await openVolumesDialog(page)
    test.skip(!opened, 'no active namespace — Welcome shown')
    const dialog = page.locator('dialog[open]')
    await expect(dialog.locator('th', { hasText: 'Name' })).toBeVisible()
    await expect(dialog.locator('th', { hasText: 'Size' })).toBeVisible()
  })

  test('footer has Snapshots and Delete All buttons', async ({ page }) => {
    const opened = await openVolumesDialog(page)
    test.skip(!opened, 'no active namespace — Welcome shown')
    await expect(page.getByRole('button', { name: 'Snapshots' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Delete All' })).toBeVisible()
  })

  test('Snapshots button opens the Snapshots dialog with export/import', async ({ page }) => {
    const opened = await openVolumesDialog(page)
    test.skip(!opened, 'no active namespace — Welcome shown')
    await page.getByRole('button', { name: 'Snapshots' }).click()
    await expect(page.getByRole('heading', { name: 'Snapshots', exact: true })).toBeVisible()
    // Export ("Create Snapshot") and import affordances must exist; they are
    // disabled unless the namespace is STOPPED, so only presence is asserted.
    await expect(page.getByRole('button', { name: 'Create Snapshot' })).toBeVisible()
    await expect(page.getByRole('button', { name: /import/i }).first()).toBeVisible()
  })
})
