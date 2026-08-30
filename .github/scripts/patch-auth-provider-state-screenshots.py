from pathlib import Path

path = Path('frontend/e2e/redesign-screenshots.spec.ts')
source = path.read_text()


def replace_once(old: str, new: str, label: str) -> None:
    global source
    count = source.count(old)
    if count != 1:
        raise SystemExit(f'{label}: expected one match, found {count}')
    source = source.replace(old, new, 1)

replace_once(
    "const dashboardFailurePages = new WeakSet<Page>()\n",
    "const dashboardFailurePages = new WeakSet<Page>()\nconst authProviderTestFailurePages = new WeakSet<Page>()\n",
    'auth provider failure state',
)

replace_once(
    """    const url = new URL(request.url())
    if (instancesStatePages.has(page) && url.pathname === '/api/v1/instances' && request.method() === 'GET') {
""",
    """    const url = new URL(request.url())
    if (authProviderTestFailurePages.has(page) && url.pathname === '/api/v1/admin/auth/providers/authentik/test' && request.method() === 'POST') {
      await route.fulfill({ status: 502, headers: corsHeaders, body: JSON.stringify({ error: 'Representative OIDC provider test failure for visual QA.' }) })
      return
    }
    if (instancesStatePages.has(page) && url.pathname === '/api/v1/instances' && request.method() === 'GET') {
""",
    'auth provider test failure route',
)

anchor = "\ntest('downloads lifecycle and files screenshot', async ({ page }, testInfo) => {\n"
addition = """

test('authentication provider add modal screenshot', async ({ page }, testInfo) => {
  await page.goto('/admin/authentication', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.locator('[data-testid="authentication-providers"]')).toContainText('Authentik')
  await page.getByRole('button', { name: 'Add provider' }).first().click()
  await expect(page.getByText('Add OIDC provider', { exact: true })).toBeVisible()
  await expect(page.getByRole('textbox', { name: 'Issuer URL' })).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/authentication-provider-add.png`, fullPage: true, animations: 'disabled' })
})


test('authentication provider edit test states screenshots', async ({ page }, testInfo) => {
  authProviderTestFailurePages.add(page)
  await page.goto('/admin/authentication', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const providers = page.locator('[data-testid="authentication-providers"]')
  await expect(providers).toContainText('Authentik')
  await expect(providers).toContainText('/api/v1/auth/oidc/authentik/callback')
  await page.getByRole('button', { name: 'Edit' }).first().click()
  await expect(page.getByText('Edit OIDC provider', { exact: true })).toBeVisible()
  await expect(page.getByText('Replace client secret', { exact: true })).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/authentication-provider-edit.png`, fullPage: true, animations: 'disabled' })

  await page.getByRole('button', { name: 'Test configuration' }).click()
  await expect(page.locator('[data-testid="provider-test-error"]')).toContainText('Representative OIDC provider test failure for visual QA.')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/authentication-provider-test-failure.png`, fullPage: true, animations: 'disabled' })

  authProviderTestFailurePages.delete(page)
  await page.getByRole('button', { name: 'Test configuration' }).click()
  await expect(page.locator('[data-testid="provider-test-success"]')).toContainText('Provider configuration test passed.')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/authentication-provider-test-success.png`, fullPage: true, animations: 'disabled' })
})


test('authentication provider delete confirmation screenshot', async ({ page }, testInfo) => {
  await page.goto('/admin/authentication', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.locator('[data-testid="authentication-providers"]')).toContainText('Authentik')
  await page.getByRole('button', { name: 'Delete' }).first().click()
  await expect(page.locator('[data-testid="confirmation-confirm"]')).toContainText('Delete provider')
  await expect(page.getByText('External identities linked to this provider will also be removed.', { exact: false })).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/authentication-provider-delete-confirmation.png`, fullPage: true, animations: 'disabled' })
})
"""
replace_once(anchor, addition + anchor, 'auth provider visual states')
path.write_text(source)
