import { test, expect, type Page } from '@playwright/test'

/**
 * Root renders the Dashboard when a namespace is active and the Welcome
 * screen otherwise (desktop mode with no selection). Namespace-dependent
 * tests skip EXPLICITLY when Welcome is shown — no silent passes.
 *
 * Returns true when the namespace sidebar is present.
 */
async function gotoRoot(page: Page): Promise<boolean> {
  await page.goto('/')
  const sidebar = page.locator('aside')
  const welcome = page.getByText('Welcome To Citeck Launcher!')
  await expect(sidebar.or(welcome)).toBeVisible({ timeout: 15_000 })
  return sidebar.isVisible()
}

test.describe('Dashboard', () => {
  test('page loads with tab bar', async ({ page }) => {
    await gotoRoot(page)
    const homeTab = page.locator('[class*="cursor-pointer"]').first()
    await expect(homeTab).toBeVisible()
  })

  test('shows dashboard sidebar or welcome screen', async ({ page }) => {
    // gotoRoot asserts that exactly one of the two screens rendered.
    await gotoRoot(page)
  })

  test('sidebar footer buttons exist when namespace is loaded', async ({ page }) => {
    const hasNamespace = await gotoRoot(page)
    test.skip(!hasNamespace, 'no active namespace — Welcome shown')
    for (const label of ['Volumes', 'Secrets', 'Launcher Logs', 'System Dump']) {
      await expect(page.getByRole('button', { name: label })).toBeVisible()
    }
  })

  test('clicking Volumes opens the Volumes dialog', async ({ page }) => {
    const hasNamespace = await gotoRoot(page)
    test.skip(!hasNamespace, 'no active namespace — Welcome shown')
    await page.getByRole('button', { name: 'Volumes' }).click()
    await expect(page.getByRole('heading', { name: 'Volumes', exact: true })).toBeVisible()
    // Dialog footer affordances
    await expect(page.getByRole('button', { name: 'Snapshots' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Delete All' })).toBeVisible()
  })

  test('clicking Secrets opens the Auth Secrets dialog', async ({ page }) => {
    const hasNamespace = await gotoRoot(page)
    test.skip(!hasNamespace, 'no active namespace — Welcome shown')
    await page.getByRole('button', { name: 'Secrets' }).click()
    await expect(page.getByRole('heading', { name: 'Auth Secrets' })).toBeVisible()
  })

  test('settings button opens config page', async ({ page }) => {
    const hasNamespace = await gotoRoot(page)
    test.skip(!hasNamespace, 'no active namespace — /config redirects to root')
    await page.getByRole('button', { name: 'Settings' }).click()
    await expect(page).toHaveURL('/config')
  })

  test('theme toggle works', async ({ page }) => {
    await gotoRoot(page)
    const html = page.locator('html')
    const themeBtn = page.locator('button[title*="theme"]')
    await expect(themeBtn).toBeVisible()

    const before = await html.getAttribute('data-theme')
    await themeBtn.click()
    await expect(html).not.toHaveAttribute('data-theme', before ?? '')
  })
})
