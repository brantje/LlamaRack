from pathlib import Path

path = Path('frontend/e2e/redesign-screenshots.spec.ts')
text = path.read_text()


def replace_once(old: str, new: str, label: str) -> None:
    global text
    if new in text:
        return
    count = text.count(old)
    if count != 1:
        raise SystemExit(f'{label}: expected one match, found {count}')
    text = text.replace(old, new, 1)


replace_once(
    "const dashboardFailurePages = new WeakSet<Page>()\n",
    "const dashboardFailurePages = new WeakSet<Page>()\nconst adminSummaryFailurePages = new WeakSet<Page>()\n",
    'admin dashboard E2E state flag',
)

route_anchor = """    if (instancesStatePages.has(page) && url.pathname === '/api/v1/imports') {"""
route_fixture = """    if (adminSummaryFailurePages.has(page) && url.pathname === '/api/v1/admin/summary') {
      await route.fulfill({ status: 503, headers: corsHeaders, body: JSON.stringify({ error: 'Administration summary temporarily unavailable for visual QA.' }) })
      return
    }
"""
replace_once(route_anchor, route_fixture + route_anchor, 'admin dashboard failure route')

test_anchor = """test('downloads lifecycle and files screenshot', async ({ page }, testInfo) => {"""
new_test = """test('administration dashboard degraded summary screenshot', async ({ page }, testInfo) => {
  adminSummaryFailurePages.add(page)
  await page.goto('/admin', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const failure = page.locator('[data-testid="admin-summary-error"]')
  await expect(failure).toContainText('Summary unavailable')
  await expect(failure).toContainText('Administration summary temporarily unavailable for visual QA.')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/admin-dashboard-summary-error.png`, fullPage: true, animations: 'disabled' })

  adminSummaryFailurePages.delete(page)
  await failure.getByRole('button', { name: 'Retry', exact: true }).click()
  await expect(page.locator('[data-testid="admin-summary-cards"]')).toBeVisible()
  await expect(failure).toBeHidden()
})


"""
replace_once(test_anchor, new_test + test_anchor, 'admin dashboard degraded screenshot test')

path.write_text(text)
